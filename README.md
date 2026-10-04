# redis-go

A Redis server written from scratch in Go, built one layer at a time.

The goal is not to replace Redis. It is to understand it. Every decision in this repo is
written down below with its reasoning.

## What works today

- An in-memory key-value store: `Set`, `Get`, `Delete`, `Keys`, `Count`
- Key expiration: `Expire`, `TTL`, `Persist`, with lazy eviction
- Safe concurrent access from many goroutines at once
- A TCP server, one goroutine per connection
- A full RESP protocol reader (the wire format real Redis clients speak)
- A full RESP protocol writer, with validation and buffering

The server currently echoes each command back as a RESP array. Command
execution is the next layer.

## Running it

```sh
go run .                 # starts the server on :6380
```

Talk to it from another terminal. `printf` is required here, not `echo`. The
protocol needs real carriage-return and line-feed bytes, and `printf` is what
converts `\r\n` from two characters into two bytes:

```sh
printf '*3\r\n$3\r\nSET\r\n$4\r\nname\r\n$6\r\ngopher\r\n' | nc localhost 6380
```

Tests:

```sh
go test ./...            # all tests
go test -v ./...         # with t.Log output
go test -race ./...      # with the data race detector
go test -count=1 ./...   # bypass the test result cache
```

---

# Part 1: Concepts

Basic Go syntax is assumed. What follows is the non-obvious material, the things
that decide whether the code reads as arbitrary or as inevitable.

## Go

**Slice headers, and why a struct can contain a slice of itself.** A slice value
is a fixed-size header pointing at elements stored elsewhere, so a struct's size
never depends on how many elements it holds. Indirection through a pointer, map,
or func does the same job; an array does not, because it stores elements inline.

**Structs that must not be copied.** Once a struct holds a `sync.Mutex`, copying
it produces a second lock protecting the same data, which protects nothing.
`go vet` catches the obvious cases; pointer receivers everywhere prevent the
rest.

**Mutexes are not reentrant.** A goroutine that already holds a lock and takes
it again deadlocks against itself. Any codebase with locks needs an explicit
convention about which functions lock and which assume a held lock.

**What a blocked goroutine actually costs.** A goroutine waiting on a socket is
parked by the runtime's network poller: its OS thread is released, and what
remains is a small stack and a scheduler entry. This is the whole reason
goroutine-per-connection is viable where thread-per-connection is not.

**`bufio` buffer ownership.** A buffered reader holds bytes beyond the message
you just parsed, so the reader must outlive any single read. `ReadSlice` hands
back a view into that internal buffer, valid only until the next read, which is
why parsed strings must be copied out.

**Sticky errors.** A buffered writer records its first failure, refuses
subsequent writes, and returns that error from `Flush`. This turns a dozen
error checks into one, at the cost of knowing exactly where the failure
happened.

**`io.EOF` versus `io.ErrUnexpectedEOF`.** The standard library distinguishes a
stream that ended at a clean boundary from one that ended mid-value, and
`io.ReadFull` codifies the rule. Any parser reading framed data needs the same
distinction.

**Error identity versus error text.** `errors.Is` walks the chain built by `%w`,
so an error can gain context at every layer and still be recognized at the top
by the sentinel it wraps.

**Allocation-aware conversions.** `strconv.Itoa` allocates; `AppendInt` into a
reused buffer does not. On a path that runs once per reply, that difference is
the difference between garbage and none.

**`t.Fatalf` outside the test goroutine.** It calls `runtime.Goexit`, which
unwinds only the calling goroutine, so a test can hang or wrongly pass.
`t.Errorf` is safe from anywhere.

## REdis Serialization Protocol (RESP)

**Five types, identified by the first byte.** Simple string `+`, error `-`,
integer `:`, bulk string `$`, array `*`.

**Length-prefixed framing.** `$5\r\nhello\r\n` declares its size before its
data, so a value can contain any bytes at all, newlines included. The parser
counts rather than searches.

**Commands are not a separate syntax.** `SET name gopher` is just an array of
bulk strings that happens to have a usable shape, which means one parser handles
both requests and replies.

**Null is a distinct answer, not an empty one.** `$-1` and `*-1` say "there is
no value", which is what makes `GET` on a missing key unambiguous.

**Error replies are data.** `-ERR unknown command` is a normal reply the server
encodes and the client decodes. Nothing about the connection has gone wrong.

## Math

**Delimiter framing versus length framing.** Searching for a terminator makes
the data's content part of the protocol; being told the size up front does not.
Every escaping scheme in every delimiter-framed protocol exists to work around
that, and length prefixes make the problem disappear.

**Recursive descent, and why it needs a bound.** One function per grammar rule,
with the call stack holding the nesting. The stack is finite and the input is
attacker-controlled, so depth must be capped explicitly.

**The critical section as a serialization point.** Work inside a lock cannot be
parallelized. The fraction of total work that is serialized puts a ceiling on
throughput no matter how many cores are added, which is why validation belongs
outside the lock and why lock scope is a design decision, not an afterthought.

**Lazy versus active cleanup.** Doing work when something is touched spreads
cost evenly and keeps every operation O(1), at the price of unbounded garbage
when nothing is touched. Doing it on a timer bounds the garbage but adds a
periodic scan. Real systems do both, weighted by how much staleness they can
tolerate.

**Amplification as the security metric.** The number that matters is the ratio
between what an attacker spends and what the server spends in response. Any
resource committed before the sender has paid for it is leverage, and a 10-byte
message that triggers a 64 MB allocation is a 6-million-to-one exchange rate.

---

# Part 2: Decisions Made

## Expiration

**Lazy eviction, with the staleness it implies accepted openly.**
Expired keys are removed when something touches them, not on a timer. Every
operation stays O(1) and no goroutine walks the keyspace, but a key nobody asks
for keeps its memory, and `Keys`/`Count` still count it. Real Redis makes the
same primary choice and then layers sampled active expiry on top to bound the
waste; that second layer belongs here too, later, and it is a bound rather than
a guarantee in both systems.

**A deadline belongs to the value, not to the key name.**
`Set` clears any existing expiration. Without that, overwriting a key whose old
deadline had already passed would store a value that disappears on the next
read, and the bug would surface only under timing that is hard to reproduce.

**Expiry state lives in a second map, not in the value.**
Keys with no TTL cost nothing, and the common path never touches the expiry map
at all. The price is two structures that must be kept consistent, which is part
of why they share one lock.

## Concurrency

**One lock for both maps, rather than one lock each.**
Most operations touch both, so two locks would mean acquiring both in a fixed
order to avoid deadlock, for no gain in parallelism. A single lock makes the
consistency between `data` and `expirations` structural instead of a rule
someone has to remember.

**`sync.Mutex`, not `sync.RWMutex`.**
`RWMutex` would only help if reads genuinely outnumbered writes and were
actually read-only. Here `Get` deletes expired keys, so the hot read path
mutates and needs exclusive access anyway. Only `Keys` and `Count` could share,
and those are the rare O(n) operations. `RWMutex` has a higher uncontended cost,
so it would be a net loss.

**Exported methods lock; unexported helpers assume the lock is held.**
Go mutexes are not reentrant, so without a stated convention this becomes a
self-deadlock waiting to happen the first time someone reuses a helper. The rule
is cheap to state and removes the entire class.

**Validation runs before the lock is taken.**
Rejected input never enters the critical section, so a caller sending garbage
cannot make every other goroutine queue behind it. Lock scope is treated as a
throughput decision, not an afterthought.

## Networking

**Listening, serving, and running are separate functions.**
The split exists for testability, and it pays for itself immediately: tests bind
`:0`, let the OS assign a free port, and drive the accept loop directly. The
suite can then start real servers repeatedly and in parallel with no fixed-port
collisions and no sleeps.

**Goroutine-per-connection, with a read loop inside each.**
This scales because a blocked goroutine releases its OS thread, so idle
connections cost a stack and a scheduler entry rather than a thread. The inner
loop is what makes a connection long-lived, which is the entire reason a client
can amortize one TCP handshake over thousands of commands.

**The reader and writer are owned by the connection, created once.**
A buffered reader may already hold the next command's bytes, so constructing a
second one mid-connection silently strands them. Tying buffer lifetime to
connection lifetime makes that unrepresentable rather than merely discouraged.

**Backpressure is per-connection and implicit.**
A client that stops reading fills its socket buffer and blocks that connection's
`Flush`, and only that goroutine. No queue grows without bound, and no other
client is affected.

## The protocol

**Length framing, replacing the delimiter framing the server started with.**
Splitting on newlines made the data's content part of the protocol, so any value
containing a newline corrupted the stream. Counting bytes removes the coupling
entirely and makes escaping unnecessary rather than merely careful.

**One struct for all five types, not an interface with five implementations.**
Go has no sum type. The interface version moves the type switch to every call
site as an assertion and makes the zero value meaningless. One struct with a
discriminant field keeps parser and encoder flat and switchable, at the cost of
fields that are dead for any given type, which validation then has to police.

**Three independent defenses against attacker-chosen lengths.**
A cap on bulk string size, a cap on array length, and no preallocation when
reading arrays. The caps reject absurd claims from the header alone; the third
is the one that matters, because it makes memory track elements that actually
arrived rather than elements that were merely promised. Bulk strings cannot use
that trick, since the buffer must exist before the read, which is exactly why
their cap is the only line of defense and is pinned to Redis's own value.

**Recursion depth is capped, because the input controls it.**
Nesting is held on the call stack, the stack is finite, and the sender picks the
nesting. Without a bound, a few bytes per level buy a stack overflow.

**A clean disconnect and a truncated message are separate outcomes.**
Ending between values is a client leaving; ending inside one is a failure. The
conversion happens at the exact point where the type byte has been consumed,
which is the only place that distinction is knowable, and only the second is
logged. Without this, normal churn would bury real faults in noise.

**Syntax and shape are validated in separate passes.**
One answers "is this well-formed RESP", the other "is this a command". Fusing
them would be fewer lines and would also make the parser useless for reading
replies, which is precisely what the round-trip tests do with it.

**Invalid replies are made unrepresentable rather than merely checked.**
Most combinations of the five fields are nonsense. Constructor helpers can only
produce coherent ones, and they sanitize line breaks at construction, so an
error message assembled from user input cannot carry a `\r\n` that would let a
client read an injected second reply.

**Encoding is all-or-nothing, and the failure mode is why.**
A partially written value does not corrupt one reply, it desynchronizes the
stream: the client reads the remainder as the start of the next message and
every subsequent reply is wrong. Validating the whole tree before emitting any
of it keeps the failure local and recoverable.

**Small write errors are unchecked on purpose.**
The buffered writer is sticky, so one check at `Flush` catches what a dozen
inline checks would, trading precise failure location for code that stays
readable. Batching those writes into a single flush also keeps a reply to one
syscall.

**A protocol error closes the connection after replying.**
Once framing is violated the position in the stream is unknown, so there is no
safe resynchronization point. The client is told why, then hung up on, and the
errors from that final write are deliberately discarded.

## Testing

**Unexported internals are tested directly**, by keeping tests in the same
package. The parser's helpers and the store's expiry check are where the subtle
bugs live, and testing only the public surface would reach them indirectly at
best.

**Round-trip tests over hand-written byte expectations.**
Encoding every value type and decoding it back catches disagreement between the
two halves without pinning down bytes that are allowed to change. The exact-byte
tests still exist, but they cover the wire format, not the invariant.

**Boundary values are in the table on purpose**, including `MaxInt64`,
`MinInt64`, empty strings, embedded CRLF, and null forms. Those are where
encoders and parsers disagree.

**Real servers on OS-assigned ports, with read deadlines**, so the suite runs
repeatedly and in parallel without fixed-port collisions, and a hung server
fails in seconds instead of blocking the run.

**`-race` is part of the normal workflow**, because the failure it catches is
probabilistic. A missing lock can pass a hundred runs and fail in production.

---

## Known gaps

Honest list of things that are wrong or missing on purpose, to be addressed as
the server grows:

- `Persist` does not check `isExpired` first, unlike `Get`, `Expire`, and
  `TTL`. A key that is past its deadline but not yet reaped can be revived by
  calling `Persist` on it.
- `Keys` and `Count` include expired-but-not-yet-removed keys.
- The server echoes commands instead of executing them.
- Nothing is persisted; all data is lost when the process exits.

## Where this is going

Roughly in order: a command dispatcher wiring `PING`/`SET`/`GET`/`DEL`/`EXPIRE`
end to end, compatibility with the real `redis-cli`, active background
expiration, snapshotting to disk, an append-only log for crash recovery, then
the remaining data types (lists, hashes, sets) and pub/sub.

Each layer adds its concepts to Part 1 and its reasoning to Part 2 above.

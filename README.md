# Inverz

<!-- markdownlint-disable-next-line MD033 -->
<img src="logo.svg" alt="Inverz Logo" align="right" width="128">

> Reverse DayZ and BattlEye player identifiers back into SteamID64.  
> Because apparently storing a few billion hashes was the boring solution.

Ever looked at a DayZ GUID or a BattlEye GUID in a server log and thought:

> **"Cool. But who the hell is this on Steam?"**

Normally, the answer is: you don't know.

DayZ and BattlEye deliberately expose derived identifiers
instead of the original SteamID64:

* DayZ: `Base64URL(SHA256(decimal SteamID64))`
* BattlEye: `MD5("BE" + little-endian SteamID64)`

These are one-way hashes. There is no mathematical inverse.

Fortunately, Steam IDs live in a very finite, very boring integer space.

Inverz takes advantage of that.

<!-- markdownlint-disable-next-line MD033 -->
## A few billion IDs - about 5 GiB <br clear="right"/>

The default index build covers $2^{31}$ AccountID values
(from 0 through $2^{31}-1$).
Precomputing the mappings and dumping them into a database works.

It is also hilariously wasteful.

Storing just the raw binary hash and SteamID64 pairs would take approximately:

* BattlEye: $24 \times 2^{31}$ bytes, or 51.5 GB.
* DayZ: $40 \times 2^{31}$ bytes, or 85.9 GB.
* Both algorithms: about 137 GB.

These are raw "hash + SteamID64" pairs, before SQLite pages, B-trees, metadata,
alignment, journaling, and everything else a real database wants.

A real indexed database would take even more.
This is the default build range,
not the full AccountID range representable by SteamID64.

Inverz needs about **5.2 GB (4.9 GiB)** per complete index.

* No giant hash database.
* No SHA-256 stored two billion times.
* No enormous SQL index.

Just enough information to find a small candidate set,
then verify every candidate against the full hash.

## How?

### Hash-prefix buckets

The first 18 hash bits select one of $2^{18} = 262{,}144$ buckets.
For DayZ, these are exactly the first three Base64URL characters.
For BattlEye, they are four hexadecimal characters
and the two most significant bits of the fifth.
The identifier points straight to its bucket.
No rummaging through the rest of the index.

Assuming a uniform hash distribution over $2^{31}$ AccountID values,
the expected number of candidates in a bucket is:

$$
\frac{2^{31}}{2^{18}} = 8192
$$

That is an average, not a promise about every bucket.
A 262,144-fold reduction in the search space
also does not mean a 262,144-fold speedup:
storage reads, decoding, and hashing candidates still take time.
Every candidate gets a full hash check,
so a matching prefix alone doesn't count.

### Delta and Golomb–Rice coding

AccountIDs in each bucket are sorted.
Instead of storing full values,
the index stores distances between adjacent values (delta encoding).
For nearly uniformly distributed candidates,
those distances are well suited to Golomb–Rice coding:
the quotient is written in unary and the remainder uses a fixed `k` bits.

With `p18-k17`, each entry averages about 19.54 bits:
roughly 4.89 GiB of Rice payload for $2^{31}$ values, plus a little metadata.

The hashes themselves?
They look random, so conventional compression has almost nothing to grab.
So Inverz doesn't bother storing them.
That's the whole trick.

### One-pass generation

The obvious generator would hash every ID once to count buckets,
then do it all again to fill them.
Four billion hashes and a massive intermediate array.
No thanks.

Inverz processes IDs in bounded, sequential blocks.
Hash workers calculate buckets in parallel;
completed blocks are put back in AccountID order before encoding.
The encoder can write deltas immediately,
with no global sort and no giant array of IDs sitting around.

The DayZ generator advances the decimal SteamID64 representation incrementally.
Buckets are split into independent shards,
and the word-oriented Rice writer packs multiple bits per operation.
The worker count changes parallelism, not the output bytes.

On a Ryzen 9 5950X, local builds of the full default range -
all 2,147,483,648 values - take about 29-32 seconds,
roughly 70 million IDs per second.
Your hardware will have opinions of its own.
Still: billions of hashes, encoded and written to disk, in about half a minute.

For scale, writing 85.9 GB at a hypothetical steady
500 MB/s takes almost three minutes.
That is just writing the raw DayZ data.
No hashing, no database, no benchmark competitor -
just the arithmetic of moving that much stuff.

## Fast enough to be boring

Cold lookup is the uncached route: read the relevant bucket,
decode an average of about 8,192 candidates,
recompute their MD5 or SHA-256, and find the exact match.
The whole multi-gigabyte index never needs to sit in RAM.

The CLI keeps positive results in a bounded SQLite cache by default.
SQLite cache candidates are checked against the full hash;
the process L1 holds only previously verified results.
Repeat a lookup and the expensive path can disappear.
Disable caching with `--no-cache`.

Example lookup times amd64 with a Ryzen 9 5950X:

* Index lookup, application cache disabled: about `0.8 ms`
* Verified SQLite cache hit: about `20 µs`
* Process-local L1 cache hit: about `40 ns`

These Go benchmark timings exclude input parsing and CLI startup.
The fixture matches the default profile's approximate bucket candidate count,
and the filesystem cache was warm.
Treat the results as examples, not a promise for different hardware or storage.
Every index candidate is still checked against the full hash.

The point is a reverse lookup that runs on ordinary hardware
instead of requiring a database server with an existential crisis.

## Immutable indexes

The index is split into deterministic, immutable segments.
With `s28`, each full segment covers $2^{28}$ AccountIDs,
so the standard range fits in exactly eight files.

Given the same inputs, segment bytes are identical
no matter how many workers you throw at generation.
Existing segments can be checked by SHA-256 and reused.
Extend coverage? Add new files.
The old multi-gigabyte chunks don't need to be rebuilt or downloaded again.
That's when "immutable" starts paying rent.

## OCI: container infrastructure, hold the containers

How do you ship multi-gigabyte index files?
Invent archives, checksum files, versions, updates, and an auth system?
Or use the registry infrastructure that already handles all of that.  
Inverz publishes indexes as OCI artifacts:
the release manifest describes the profile,
and each segment is a separate content-addressed blob.
GHCR or another compatible registry handles storage,
authentication, and digest checks.

Already-present registry segments are not uploaded again.
On pull, existing local segments can be reused too.
Once the index is installed, lookups are local;
the registry can take the day off.

And no, Docker is not required.  
No containers, no volume mounts, no separate file host.
Just OCI, doing a job it is perfectly capable of doing.

## Profiles: three letters, several trade-offs

An index profile looks like `v1-p18-k17-s28`:
a format version and three parameters, each charging a different bill:

* `p` is the number of leading hash bits used to select a bucket.
  A larger `p` means fewer candidates per lookup,
  but more index data and bucket metadata.
* `k` is the number of Golomb–Rice remainder bits.
  It balances the unary quotient length against the remainder width.
  `k = p - 1` is a useful heuristic for nearly uniform hashes,
  but the format does not require it.
* `s` is the number of AccountID bits in a segment, so its size is $2^s$.
  It affects file size, peak generation memory, and the number of segments.

For $N = 2^{31}$, the average candidate count is $N / 2^p$:

* `p17-k16`: 16,384 candidates; about 4.64 GiB of Rice payload.
* `p18-k17`: 8,192 candidates; about 4.89 GiB of Rice payload.
* `p19-k18`: 4,096 candidates; about 5.14 GiB of Rice payload.

These size estimates include only the Rice payload,
not headers, the directory, or other metadata.
Near the optimal `k`, each extra `p` bit costs
about 256 MiB of payload for the full range.

For nearly uniform gaps, the expected encoded length is approximately:

$$
\mathbb{E}[\ell] \approx k + 1 + \frac{1}{e^{2^{k-p}} - 1}
$$

For `p18`, `k16` gives about 20.52 bits per ID,
`k17` gives 19.54, and `k18` gives 19.58.  
Segment size can also change:
`s27` gives 16 segments instead of eight, while `s29` gives four.
The total payload changes little.

So `p` buys lookup speed with disk,
`k` haggles with entropy,
and `s` decides how big the chunks are.
The default `p18-k17-s28` is a compromise,
not a sacred combination. For now, it is a pretty good one.

## Usage

The CLI generates indexes, pulls and publishes OCI artifacts,
calculates DayZ and BattlEye identifiers,
resolves them, and manages local indexes.

The full command reference is in [CLI.md](CLI.md).

The shortest useful trip:

```sh
# Download the prebuilt DayZ index
inverz pull dz

# Hash a SteamID64 and resolve the resulting DayZ ID
inverz query dz "$(inverz hash dz 76561198000000000)"
```

And back comes the original SteamID64.
`hash` prints only the identifier, so the shell can pass it straight to `query`.

Not sure whether to pull or generate?  
Try `inverz generate dz --estimate` for resource requirements.
Then pick whichever is cheaper on your machine:
CPU or network. One box's timing says nothing about another box.

## Go API

The root `inverz` package builds, opens, queries, inspects, verifies, lists,
and removes local indexes. Cache support is an interface.
OCI transport lives separately in `github.com/woozymasta/inverz/oci`.

```go
package example

import (
    "context"

    "github.com/woozymasta/inverz"
)

func resolve(root, dayzID string) (uint64, error) {
    index, err := inverz.Open(root, inverz.AlgorithmDayZ, inverz.DefaultProfile())
    if err != nil {
        return 0, err
    }
    defer index.Close()

    result, err := index.Resolve(context.Background(), dayzID)
    if err != nil {
        return 0, err
    }
    return result.SteamID, nil
}
```

## Why?

No particularly good reason.

I wanted to know whether billions of cryptographic identifier mappings
could be made small enough to distribute and cheap enough to query
on an ordinary server, without maintaining a ridiculous database.

Turns out they can.

So now this exists.

## What Inverz is not

Inverz does not "decrypt" MD5 or SHA-256,
and it does not break either hash function.

It searches a known, bounded input range
using a prebuilt compact index and verifies the result against the full hash.

The task is not to reverse the hash function.
It is to know exactly where to look.

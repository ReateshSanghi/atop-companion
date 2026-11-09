# ATOP Advanced Cross-Platform Companion

A desktop GUI (written in Go, using [Fyne](https://fyne.io)) for reading **offline Linux [ATOP](https://www.atoptool.nl/) raw log files**. Point it at a folder, and it plots CPU, memory, load, disk and network history over time, highlights every period where a value goes over its limit, and shows the busiest processes for any point you choose.

> Written for: developers taking over this codebase who need to understand what it does before modifying, extending or rewriting it.

---

## Table of contents

1. [What it does](#what-it-does)
2. [Requirements and build](#requirements-and-build)
3. [Project layout](#project-layout)
4. [How it works](#how-it-works)
5. [Binary format (atop 2.8)](#binary-format-atop-28)
6. [Thresholds and anomalies](#thresholds-and-anomalies)
7. [Local storage](#local-storage)
8. [File validation and quarantine](#file-validation-and-quarantine)
9. [Tests](#tests)
10. [Known issues and limitations](#known-issues-and-limitations)
11. [Suggested next steps](#suggested-next-steps)

---

## What it does

When the application starts it:

1. Opens (or creates) the embedded database `atop_historical_data.db` in the **current working directory**. The database is locked while the app runs, so only one copy can run from a given folder.
2. Deletes stored samples older than 7 days (`storage.RetentionDays`).
3. Opens the main window.

### Main window

| Area | Behaviour |
|---|---|
| **Open folder...** | Loads every atop raw log in a folder. Files are recognised by their magic number, so names do not matter. Other files are ignored. Files that cannot be read are listed in a dialog. |
| **Host** selector | Shown only when the folder contains logs from more than one host. |
| **Plots** (left) | The legend of available plots, with a checkbox for each. Each plot has one unit, so plots with different units are never drawn on the same axis. |
| **Charts** (right) | One chart per selected plot, with the legend of its series at the top. The Y axis uses the values of the visible range, so a flat signal is not squashed. The X axis is local time. |
| **Drag** on a chart | Selects a time range. It applies to every chart at once. |
| **Double-click** on a chart, or **Reset zoom** | Returns to the full range. |
| **Click** or **right-click** on a point | Opens a window with every value at that sample, the disks, the network interfaces and the busiest processes. |
| **Hover** over a red zone | Shows a summary of the values that are over their limit at that sample. |
| **Analyze this !!** | Lists every episode where a value went over its limit. **Show** zooms to the episode and opens the details at its peak. |
| **Dark mode** | Switches the whole window, including the charts, between light and dark. |
| **Clear log cache** | Empties the database. Loaded data stays on screen. |

Plots: CPU (busy, iowait, steal), memory (% used and swap %), memory used (GiB), load average, disk busy %, disk throughput (MB/s), and network (KB/s per interface, received and transmitted).

**Supported input:** atop **2.8** raw logs (`/var/log/atop/atop_YYYYMMDD`). Other versions are listed as not read.

**Not yet available:** NFS statistics (the counters exist in the file but are zero on the test host, and their offset is not verified), and swap or network data for hosts that do not report them.

---

## Requirements and build

- **Go**: `go.mod` declares `go 1.26.7`. Go 1.27 is installed on the machine where this README was written.
- **C compiler**: Fyne uses cgo (OpenGL / GLFW). On Windows install a MinGW-w64 toolchain and make sure `gcc` is on `PATH`. This was verified with WinLibs (GCC 16.2.0, UCRT), installed via `winget install BrechtSanders.WinLibs.POSIX.UCRT`. Open a new shell after installing so `PATH` is refreshed.
- **Dependencies** (from `go.mod`):
  - `fyne.io/fyne/v2` v2.8.1 — GUI toolkit
  - `go.etcd.io/bbolt` v1.5.0 — embedded key/value store

```powershell
# from the project root
go mod download
go build -o atop-companion.exe .      # add -ldflags "-H windowsgui" for no console window
.\atop-companion.exe
```

Or run directly during development:

```powershell
go run .
```

The database file is created relative to the directory you run the program from. Run it from the project root unless you want the data elsewhere.

---

## Project layout

```
atop-companion/
├── main.go                  # Entry point: opens the cache, prunes it, runs the UI
├── go.mod / go.sum          # Module definition (module name: atop-companion)
├── File_Structure.ps1       # PowerShell script that scaffolds the folder tree (historical)
├── atop_historical_data.db  # bbolt database created at runtime
├── telemetry/
│   ├── header.go            # Reads and validates the atop file header
│   ├── layout.go            # Byte offsets of the statistics block per atop release (2.8 only)
│   ├── engine.go            # Walks the records of one file and decodes the system figures
│   ├── procs.go             # Per-process block: decoded on demand for the busiest processes
│   ├── loader.go            # Reads a whole folder in parallel and merges it per host
│   ├── series.go            # Builds plots (one per unit) and finds anomalies over the limits
│   ├── types.go             # Sample, DiskStat, NetStat, ProcessStat types
│   └── engine_test.go       # Tests with generated binary fixtures
├── ui/
│   ├── dashboard.go         # Main window, folder loading, plot checklist, hover popup
│   ├── components.go        # The chart widget: drawing, axes, zoom, hover and click
│   ├── modals.go            # Point details window and the "Analyze this !!" window
│   └── theme.go             # Light and dark appearance and the chart colours
├── storage/
│   ├── bbolt_wrapper.go     # Database: batch save, prune, query, clear
│   ├── bbolt_wrapper_test.go# Retention tests
│   └── guardrails.go        # Magic-number checks for dump files, quarantine + audit log
├── reports/                 # Empty placeholder (svg_generator.go) for the planned SVG export
```

Notes:

- `reports/` is empty and unused. Because it has no Go file with a package clause, `go vet ./...` reports an error for it; `go build` and the tests of the other packages are unaffected.
- `telemetry.ProcessDetail` is defined but never used.

---

## How it works

### Loading a folder

```
LoadFolder(dir)
   │  list files; keep those whose first bytes are the atop magic (header check only)
   ▼
   worker pool (one worker per CPU): ReadFile(path) for each log, in parallel
   │   ReadAtopHeader           magic, version, header and record sizes
   │   findLayout               unknown version → reported as not read
   │   for each record:
   │     read the 96-byte record header (time, compressed lengths, interval)
   │     skip records with interval 0 (first record after boot: totals since boot)
   │     inflate the system block, check its length, decode CPU, memory, swap,
   │       load, disks and network; a failing record is skipped and counted
   │     remember where the process block is (decoded later, see below)
   ▼
merge per host, sort by time, drop duplicate timestamps (overlapping logs)
   ▼
BuildPlots(samples)  →  FindAnomalies(samples, plots)  →  charts
```

The folder used for testing (three daily logs, 353 samples) loads in about 0.15 s on the test machine.

### Processes

The process block is large (about 54 MB uncompressed per record), so it is not decoded during loading. When you open the details of a point, `Sample.Processes()` reads and decodes the block of that record only (about 80 ms). It keeps the 20 busiest process leaders, sorted by CPU.

### Charts

Each chart draws into a raster image, so drawing stays fast with thousands of points. Lines are broken where samples are missing or where a gap is more than three intervals long. Red columns mark every sample where a series is over its limit. Dotted red segments show the limit itself.

---

## Binary format (atop 2.8)

The layout was taken from `pam_monitoring_tool/src/atop_parser/reader.py`, which decodes atop 2.8 logs natively. The per-process record size (968 bytes) was taken from the atop 2.8.1 source headers. This Go code was checked against the three atop 2.8 files in `pam_monitoring_tool/atop_data`: the 211 records of two of them walk exactly to end-of-file, all three files decode to one sample per non-boot record (144, 144 and 65 samples), and the first sample of `atop_20260928` matches the Python decoder (CPU 14.80%, memory 60.10%, same timestamp). **No Linux machine with `atop -r` was available, so the offsets have not been compared with atop's own output.**

All integers are little-endian.

### File header (first 480 bytes)

| Offset | Type | Field | Use |
|---|---|---|---|
| 0 | u32 | magic | must be `0xFEEDBEEF` |
| 4 | u16 | version word | bit 15 marks atop 2.x; `major = (v >> 8) & 0x7F`, `minor = v & 0xFF`. `0x8208` is atop 2.8 |
| 10 | u16 | header length | the first record starts here (480 in the test files) |
| 12 | u16 | record header length | 96 for atop 2.8 |
| 14 | u16 | clock ticks per second | usually 100 |
| 28 | u32 | sstat length | decompressed size of the system-statistics block (`1021960` for 2.8) |
| 32 | u32 | tstat size | size of one per-process entry (968 bytes); the process block is an array of these |
| 101 | 65 bytes | nodename | host name |
| 436 | u32 | page size | 4096 if zero |

### Record (repeats until end of file)

| Offset in record | Type | Field |
|---|---|---|
| 0 | u64 | curtime (epoch seconds) |
| 16 | u32 | compressed length of the system block (zlib) |
| 20 | u32 | compressed length of the process block (zlib) |
| 24 | u32 | interval in seconds that this sample covers |
| 96 | bytes | zlib-compressed system block, then the process block |

A record with `interval == 0` (or more than 86400) holds totals since boot rather than a delta, so it is skipped. In the test files this is the first record of each file.

### System-statistics block (atop 2.8)

Offsets into the **inflated** block (`layout28` in `layout.go`):

| Metric | Offset | Contents | Calculation |
|---|---|---|---|
| CPU count | 0 | u64 | must be 1..4096 |
| CPU ticks | 88 + 8·i, i = 0..7 | system, user, nice, idle, wait, irq, softirq, steal (u64 each). Guest is not added: it is already part of user | busy = (total − idle − wait) / total; iowait and steal are the same ratio for their own counters |
| Load average | 32 | 3 × f32: 1, 5 and 15 minutes | as stored |
| Memory | 344312 + 8·k | k = 0 physical, 1 free, 2 buffers, 4 cache, 6 swap total, 7 swap free, 19 reclaimable slab (pages) | `used = phys − free − buffers − cache − reclaimable`, clamped at 0 |
| Disks | 560672 + 128·j, j = 0..255 | 32-byte name, then nread, nrsect, nwrite, nwsect, io_ms (u64). The list ends at the first empty name | MB/s = sectors × 512 / 2^20 / interval; busy = io_ms / (interval × 1000) |
| Network | count u32 at 345592; entries from 345600, 272 bytes each | 16-byte name; rx bytes at +16, tx bytes at +112 | bytes/s = bytes / interval; virtual interfaces (lo, bond, docker, veth, br-, virbr, tun, tap) are skipped |

CPU, disk and network counters are per-interval deltas, which is why the interval is needed. Each record is checked before use: CPU ticks must not exceed `ncpu × interval × hertz × 1.5`, and free memory must not exceed physical memory. A record that fails either check is skipped.

### Process block (atop 2.8)

A headerless array of 968-byte entries (`struct tstat`), one per task. Offsets inside one entry, from the atop 2.8.1 source and checked against the data:

| Offset | Field | Used for |
|---|---|---|
| 4 / 8 | pid / ppid | identity |
| 44 | number of threads | threads |
| 48 | name, char[16] | process name |
| 64 | is a process leader (1) or a thread (0) | only leaders are counted |
| 65 | state character | state |
| 88 | command line, char[256] | details |
| 448 / 456 | user / system CPU ticks | CPU % = (user + system) / (interval × hertz) × 100 |

Per-process memory and disk fields are not shown yet: their units are not confirmed. See the known issues.

---

## Thresholds and anomalies

Defaults (in `telemetry/series.go`):

| Plot | Series | Limit |
|---|---|---|
| CPU | busy | 80 % |
| CPU | iowait | 20 % |
| CPU | steal | 10 % |
| Memory | used | 90 % |
| Memory | swap | 20 % |
| Disk busy | each disk | 80 % |
| Load average | 1 min | number of CPUs |

A value strictly above its limit is an anomaly. Consecutive samples over the limit form one **episode**, and an episode is listed once with its peak. Network and disk throughput have no limit. The limits are constants for now; making them editable is a planned change.

---

## Local storage

- **Engine:** [bbolt](https://github.com/etcd-io/bbolt), a single-file embedded B+tree store.
- **File:** `atop_historical_data.db` in the working directory, mode `0600`. Open timeout is 1 second. A second copy of the app started in the same folder fails with a "timeout" error while the first one is running.
- **Bucket:** `AtopHistoryBucket`.
- **Key:** `metric:<timestamp>` where the timestamp is `YYYY-MM-DD HH:MM:SS` in UTC (`storage.TimestampLayout`).
- **Value:** JSON of `storage.HistoricalSnapshot`: `timestamp`, `cpu`, `mem` and `disk_io` (MB/s, read + write over all disks).
- **Writes:** one transaction per import. The first host of a folder is saved in the background.
- **Idempotency:** re-importing overwrites samples with the same timestamp.
- **Retention:** `PruneOldLogs` runs once at startup and removes samples older than 7 days (UTC).
- The database is a cache. The charts are built from the folder, not from the database.

---

## File validation and quarantine

`ValidateLogFile` is called **only when `ReadAtopHeader` fails for a file that was selected directly**. It reads the first four bytes and:

| First 4 bytes | Meaning | Action |
|---|---|---|
| `7F 45 4C 46` (`\x7fELF`) | Linux core dump | Quarantine |
| `4D 44 4D 50` (`MDMP`) | Windows minidump | Quarantine |
| `50 41 47 45` (`PAGE`) | Windows full memory dump | Quarantine |
| anything else | — | Returns OK |

When a whole folder is loaded, files without the atop magic are ignored and nothing is quarantined. Quarantine is only used by the (currently unused) single-file check.

**Quarantine** (`QuarantineAndLog`) appends to `security_audit.log` and **moves the original file** into `./quarantine/`. The sigValidAtopRaw marker is defined but never checked.

---

## Tests

Run the unit tests from the project root:

```powershell
go test ./telemetry/ ./storage/ -v
```

| Area | What is checked |
|---|---|
| System figures | CPU, memory, swap, load, disk and network values decode from a known block; since-boot counters, implausible memory and memory overflow are rejected |
| Header | a 2.8 header is read; a file without the magic is rejected |
| Files | unsupported versions give `ErrUnsupportedVersion`; a file without records gives `ErrNoMetricRecords`; since-boot records are skipped; a corrupt record does not stop the rest |
| Plots | units are split; idle disks are left out |
| Anomalies | runs above a limit are grouped, and each run reports its peak |
| Processes | only process leaders are counted, and the busiest are kept |
| Storage | pruning removes only expired samples, including a large batch |

The fixtures are built in code, so no real ATOP log is needed. They check the code against itself; the comparison with the Python decoder above is the independent check.

---

## Known issues and limitations

- **Only atop 2.8 is supported.** Other versions are listed as not read. Adding one means registering a `RawLayout` in `layout.go`, after decoding a sample log of that version.
- **The reclaimable-slab memory index may be wrong.** The Python decoder (and this code) read word 19 for reclaimable slab. A research pass over the atop 2.8.1 source suggests word 20 is slab-reclaimable and word 19 is shared memory swapped. On the test files word 19 is 0 and word 20 is about 200,000 pages (about 0.8 GiB of 64 GiB), so the effect is small there, but it is **unverified**.
- **Per-process CPU does not fully add up to the system figure.** The sum of process CPU ticks is 65–82% of system busy ticks in the test files. The cause is not known (short-lived processes missed by sampling is a guess). Treat per-process CPU as indicative.
- **Per-process memory and disk are not shown.** The RSS field seems to be in kilobytes, not pages, and the per-process disk counters do not match the system disk counters. Both need a check against `atop -r`.
- **NFS is not decoded.** The NFS section exists in the file. On the test host its counters are zero, and its offset is estimated, not verified.
- **Network, swap and load are decoded from the layout, but swap and network have not been compared with atop's output.**
- **Default limits fire rarely on the test folder.** The three test days peak at about 27% CPU and 62% memory, so no anomaly is found. Use a busier log to see the red zones.
- **The layout is checked on three files from one host.** Other hardware or kernels could in principle differ. The plausibility checks reject most mismatches, but not all.
- **Only one copy of the app can run from a folder** (database lock).
- **Existing databases** may contain fake samples saved by an older version. Use **Clear log cache** once.
- **Charts** are fixed-height in a scroll area, so they do not stretch to fill the window. Zoom is by drag only, with no mouse wheel.
- **Hover popups** only appear over red zones. Their process list is left out to keep mouse movement responsive; use a click or right-click for it.
- **Times are shown in the local time zone**, but the decoded epoch times are exact. A log from another time zone is shown shifted to local time.
- **`storage.QueryTimeframeMetrics`**, **`telemetry.ProcessDetail`**, and **`sigValidAtopRaw`** are not used.
- **Export Interactive SVG** is not implemented.
- **No `.gitignore`**. The generated database file and `quarantine/` / `security_audit.log` would be picked up by `git add`.
- **Windows file paths**: the folder path from the dialog is converted with a simple rule for drive letters. This was tested only on Windows 11.
- **Build requires cgo** and a C compiler on `PATH`.

---

## Suggested next steps

1. **Compare with `atop -r`** on a Linux machine with atop 2.8: CPU, memory, swap, disk and network for the same interval.
2. **Resolve the open memory and process questions** (slab index, per-process RSS units, process CPU gap) using that comparison.
3. **Add NFS** once the offset is confirmed.
4. **Make the limits editable** in the UI and save them per host.
5. **Separate the layers** when restructuring: parsing (`telemetry`), persistence (`storage`), and UI (`ui`).
6. **Add a `.gitignore`** for `*.db`, `quarantine/`, `security_audit.log`, and build outputs.

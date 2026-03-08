# Network Forensics Sandbox: Context & Tools

## 1. System Context

You are operating within a specialized **Ubuntu 24.04 Docker container** designed for network traffic analysis and forensics.

* **User**: `linuxbrew` (Non-root, passwordless `sudo` enabled).
* **Package Managers**: Homebrew (system), uv (Python).
* **Python**: Virtualenv is auto-activated (`/home/linuxbrew/venv`). Libraries `scapy`, `pyshark`, `pandas` are pre-installed.

## 2. Primary Tool: pcapchu-scripts (Zeek + DuckDB)

**Use this FIRST for high-level behavioral analysis.** It converts PCAPs into a structured SQL database.

### Workflow

1. **Init**: `cd /home/linuxbrew && pcapchu-scripts init <pcap>` (Ingests PCAP, runs Zeek & pkt2flow).
2. **Meta**: `cd /home/linuxbrew && pcapchu-scripts meta` (Returns schema. **ALWAYS run first**).
3. **Query**: `cd /home/linuxbrew && pcapchu-scripts query "<SQL>"` (Execute SQL).

> **Always `cd /home/linuxbrew` before running `pcapchu-scripts`.**

### Data Model

* **Zeek Tables**: `conn`, `http`, `dns`, `files`, `ssl`, etc.
* **Flow Index**: `flow_index` (Catalogue of raw PCAP slices per flow).

### SQL Cheat Sheet (DuckDB)

* **Baseline**: `SELECT "id.orig_h", service, count(*) as c FROM conn GROUP BY 1,2 ORDER BY c DESC LIMIT 10`
* **HTTP/File Correlation**: `SELECT h.host, h.uri, f.mime_type, f.fuid FROM http h, files f WHERE list_contains(h.resp_fuids, f.fuid)`
* **Find Raw PCAP (pkt2flow)**: `SELECT file_path FROM flow_index WHERE dst_ip = '1.2.3.4' AND dst_port = 80`
* **Extracted Payloads**: `SELECT mime_type, extracted as path FROM files WHERE extracted IS NOT NULL`

## 3. Secondary Tools: Packet-Level Forensics

**Use these when SQL metadata is insufficient and you need deep packet inspection.**

### A. Tshark (CLI Wireshark)

* **Basic Read**: `tshark -r input.pcap`
* **Display Filter**: `tshark -r input.pcap -Y "http.request.method == POST"`
* **Extract Fields (CSV)**:
```bash
tshark -r input.pcap -T fields -e frame.number -e ip.src -e ip.dst -e http.host

```


* **Protocol Stats**: `tshark -r input.pcap -q -z io,phs`

### B. Python (Scapy / PyShark)

Run via `python` or `ipython`.

**Snippet: Scapy (Packet Forging/Parsing)**

```python
from scapy.all import *
packets = rdpcap("input.pcap")
packets.summary()
# Extract DNS Queries
for pkt in packets:
    if DNS in pkt and pkt[DNS].qr == 0:
        print(pkt[DNS].qd.qname)

```

**Snippet: PyShark (Tshark Wrapper)**

```python
import pyshark
# Lazy loading (efficient for large files)
cap = pyshark.FileCapture('input.pcap', display_filter='http')
for pkt in cap:
    print(pkt.http.host)

```

**Snippet: Pandas (Statistical Analysis)**
*Best used with CSVs exported from Tshark.*

```python
import pandas as pd
df = pd.read_csv('tshark_export.csv')
print(df['ip.src'].value_counts().head())

```

## 4. Operational Constraints

* **Paths**: Always use absolute paths (e.g., `/data/capture.pcap`).
* **Quoting**: In DuckDB SQL, wrap dotted fields in double quotes: `"id.orig_h"`.
* **Timestamps**: Zeek `ts` is Unix epoch. Use `to_timestamp(ts)` in SQL.
* **Flows**: The `flow_index` table links Zeek metadata to raw PCAP files. Join on IPs/Ports if needed.
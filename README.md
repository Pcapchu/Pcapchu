## Introduction

Pcapchu is an **autonomous AI agent** that investigates network packet captures (pcap files) using a multi-round **Planner → Executor** pipeline. It runs all analysis inside a Docker sandbox equipped with Zeek, DuckDB, tshark, and Scapy, then produces a structured forensics report — no manual filter-writing required.

**Key features:**

- **Natural language queries** — ask "What security threats are in this capture?" and get a structured report.
- **Multi-agent architecture** — a Planner agent creates an investigation plan; Executor agents carry out each step autonomously using [ReAct](https://arxiv.org/abs/2210.03629) (Reasoning + Acting).
- **SQL-first analysis** — pcap → Zeek logs → DuckDB tables → SQL queries, powered by [pcapchu-scripts](./pcapchu-scripts/).
- **Docker sandbox** — all commands execute inside an isolated container. No tools installed on the host.
- **Multi-round investigation** — persist sessions to SQLite; resume and run additional rounds at any time.
- **Observability** — structured logging via [slogpretty](https://github.com/Marlliton/slogpretty), optional OpenTelemetry traces/metrics/logs export.
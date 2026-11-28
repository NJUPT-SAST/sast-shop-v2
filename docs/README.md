# Backend Documentation

Index of operational, security, and feature documentation for the SAST Shop v2 backend.

| Document | Scope |
|---|---|
| [Deployment & CI](./deployment-ci.md) | GitHub Actions CI/Buf/Deploy workflows, change detection, immutable TCR image rollout, server helper contract, and incremental upgrades |
| [Admin Console](./admin.md) | Admin RBAC governance, trade rounds, whole-order correction flows, incremental migrations (`005`–`009`), and verification |
| [West Pocket Integration & Acceptance](./west-pocket.md) | West Pocket AA microservice APIs, private COS / Tencent Cloud IAI / Feishu configuration, privacy & retention policies, durable jobs, and verification |
| [Store Product Categories Design](./store-product-category-design.md) | Two-level store product category specification, concurrency lock rules, and migration contract (`migrations/010_store_product_categories.sql`) |

## Root References

- [README.md](../README.md) / [README_zh.md](../README_zh.md) — Project overview, architecture, API/DB summary, local setup, and Make commands
- [AGENTS.md](../AGENTS.md) — AI coding agent guide, module map, coding conventions, and security constraints

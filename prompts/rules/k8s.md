### Kubernetes (K8s) Rules

#### Core Principles

1.  **Production Ready**: High Availability (HA) & Self-healing.
2.  **Resource Aware**: Everything must have limits.
3.  **Secure by Default**: Least privilege.

#### Critical Criteria

- **Resources**: MUST define `requests` and `limits` for CPU and Memory.
- **Probes**: MUST have `livenessProbe` and `readinessProbe`. `startupProbe` for slow starts.
- **Security**: `securityContext`. `runAsNonRoot: true`. `readOnlyRootFilesystem: true`.
- **Images**: meaningful tags (SHA/version). NEVER use `:latest` in production.
- **Availability**: `replicas > 1` (Deployment). `podDisruptionBudget`.
- **Config**: ConfigMaps/Secrets > Env Vars hardcoded.

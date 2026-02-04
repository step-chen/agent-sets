### Kubernetes

**Principles**: HA & Self-healing. Resource limits. Least privilege.

**Criteria**:

- Resources: MUST define `requests` and `limits` for CPU/Memory.
- Probes: MUST have `livenessProbe` + `readinessProbe`. `startupProbe` for slow starts.
- Security: `runAsNonRoot: true`. `readOnlyRootFilesystem: true`.
- Images: Meaningful tags (SHA/version). NEVER `:latest` in prod.
- Availability: `replicas > 1`. Use `podDisruptionBudget`.
- Config: ConfigMaps/Secrets > hardcoded env vars.

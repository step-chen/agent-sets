### Docker

**Principles**: Minimal images. Non-root. Pin versions.

**Criteria**:

- Layers: Combine RUN commands. Cleanup apt/apk cache.
- Base Image: Use slim/alpine variants.
- Secrets: NEVER bake secrets. Use build args carefully.
- CMD/ENTRYPOINT: Prefer exec form `["executable", "param"]`.

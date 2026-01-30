### Docker Rules

#### Core Principles

1. **Minimalism**: Keep images small. Multi-stage builds.
2. **Security**: Non-root user. Regular security updates.
3. **Reproducibility**: Pin versions.

#### Critical Criteria

- **Layers**: Combine RUN commands to reduce layers. Cleanup apt/apk cache.
- **Base Image**: Use slim/alpine variants if possible and stable.
- **Secrets**: NEVER bake secrets into the image. Use build args carefully.
- **CMD/ENTRYPOINT**: Prefer exec form `["executable", "param1", "param2"]`.

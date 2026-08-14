FROM grafana/grafana:13.1.3@sha256:ab5cb380e3ff3172d6c8bd2e7cfd31cce977d2881b260e1f5bc089bf0b759b43

USER root
# renovate: datasource=custom.grafana-plugins depName=victoriametrics-logs-datasource versioning=semver
ARG VICTORIALOGS_DATASOURCE_VERSION=0.31.0
RUN grafana cli plugins install victoriametrics-logs-datasource "${VICTORIALOGS_DATASOURCE_VERSION}"
USER grafana

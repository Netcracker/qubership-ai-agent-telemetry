FROM grafana/grafana:13.1.0

USER root
# renovate: datasource=custom.grafana-plugins depName=victoriametrics-logs-datasource versioning=semver
ARG VICTORIALOGS_DATASOURCE_VERSION=0.30.1
RUN grafana cli plugins install victoriametrics-logs-datasource "${VICTORIALOGS_DATASOURCE_VERSION}"
USER grafana

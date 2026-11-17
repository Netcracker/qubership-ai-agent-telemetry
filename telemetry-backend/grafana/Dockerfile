FROM grafana/grafana:13.3.1@sha256:0615ec4fbd4072004b7d86b46ded63013a74b7d6eb1119273dd89e8fa25c13cf

USER root
# renovate: datasource=custom.grafana-plugins depName=victoriametrics-logs-datasource versioning=semver
ARG VICTORIALOGS_DATASOURCE_VERSION=0.34.0
RUN grafana cli plugins install victoriametrics-logs-datasource "${VICTORIALOGS_DATASOURCE_VERSION}"
USER grafana

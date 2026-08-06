FROM grafana/grafana:13.1.1@sha256:7cb8c64c4d57a57e734073f3cc94620adb24a0acb929bd80ba9f14017e3a975b

USER root
# renovate: datasource=custom.grafana-plugins depName=victoriametrics-logs-datasource versioning=semver
ARG VICTORIALOGS_DATASOURCE_VERSION=0.30.1
RUN grafana cli plugins install victoriametrics-logs-datasource "${VICTORIALOGS_DATASOURCE_VERSION}"
USER grafana

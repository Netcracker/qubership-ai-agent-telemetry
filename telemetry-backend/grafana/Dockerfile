FROM grafana/grafana:13.1.2@sha256:d177053ab62253815f130d81504f77063baf5fd4ca93299d6048453bd31e047a

USER root
# renovate: datasource=custom.grafana-plugins depName=victoriametrics-logs-datasource versioning=semver
ARG VICTORIALOGS_DATASOURCE_VERSION=0.30.1
RUN grafana cli plugins install victoriametrics-logs-datasource "${VICTORIALOGS_DATASOURCE_VERSION}"
USER grafana

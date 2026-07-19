FROM grafana/grafana:13.1.0

USER root
RUN grafana cli plugins install victoriametrics-logs-datasource 0.27.1
USER grafana

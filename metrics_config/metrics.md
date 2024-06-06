# Installing Metrics toolkit

## Prerequisites
- If you want to set up monitoring for an external machine, forward the following ports:
    - 3000: Grafana
    - 9090: Prometheus (only if you want to expose the raw data stream)

1. Update your system:
`sudo apt update && sudo apt upgrade -y`
    - `sudo reboot` (if necessary)
2. Install prometheus: `sudo apt install prometheus -y`
3. Enable the prometheus service: `sudo systemctl enable prometheus`
4. Start the prometheus service for the first time: `sudo systemctl start prometheus`
5. Install grafana: https://grafana.com/grafana/download?edition=oss
    - `sudo apt-get install -y adduser libfontconfig1 musl`
    - `wget https://dl.grafana.com/oss/release/grafana_10.4.2_amd64.deb`
    - `sudo dpkg -i grafana_10.4.2_amd64.deb`
6. Start the Grafana service:
`sudo systemctl start grafana-server.service`
7. Install the prometheus config file:
    - `cp metrics_config/prometheus.yml /etc/prometheus/`
8. Launch go-quai
9. Open grafana: http://localhost:3000
10. Login with default credentials:
    - user: `admin`
    - pass: `admin`
    - You can choose to change these or skip if you want. Make sure you change them if the port is forwarded externally.
11. Go to Connections -> Data sources and add Prometheus as a datasource.
12. Go to Dashboards -> New -> Import, and paste the config from `metrics_config/grafana_metrics.json`
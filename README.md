example: `/opt/gofaxip-bridge/gofaxip-bridge -path="/var/log/gofaxip/xferfaxlog" -spoolerPath="/var/spool/hylafax" -logDir="./log" -lokiURL="http://your-loki-server:3100/loki/api/v1/push" -lokiUser="your_loki_username" -lokiPass="your_loki_password"`


# GoFaxIP-Bridge README (WIP)

## Overview

GoFaxIP-Bridge is a tool designed for interfacing with FreeSWITCH to enable the reception and transmission of SIP-based faxes using HylaFAX. It acts as a bridge, connecting to other PBXes, and facilitates the handling of T38 faxes, overcoming the challenge of live transcoding.

## Prerequisites

- Linux-based system
- Go (Golang) installed
- FreeSWITCH with fax capabilities
- HylaFAX installed and configured
- Access to a SIP trunk or PBX for fax transmission
- GoFaxIP

## Installation

**Clone the Repository:**

```shell
git clone [REPOSITORY_URL]
cd [REPOSITORY_DIRECTORY]
```

**Build the Application:**

```shell
go build
```

## Configuration

Configure the application using the following flags:

- `path`: Path to the FreeSWITCH log file for fax transactions (default: /var/log/freeswitch/xferfaxlog)
- `spoolerPath`: Path to the HylaFAX spooler directory (default: /var/spool/hylafax)
- `logDir`: Path to the directory for storing application logs (default: ./log)
- `lokiURL`: URL to Loki's push API for advanced log management (optional)
- `lokiUser`: Username for Loki (if Loki is used)
- `lokiPass`: Password for Loki (if Loki is used)

## Running the Application

To start the bridge, run the built binary with the necessary flags:

```shell
./[BINARY_NAME] -path=[LOG_FILE_PATH] -spoolerPath=[SPOOLER_PATH] -logDir=[LOG_DIR] -lokiURL=[LOKI_URL] -lokiUser=[LOKI_USER] -lokiPass=[LOKI_PASS]
```

## Setting Up as a Linux Service

**Create a Systemd Service File:**

Go to /etc/systemd/system/. Create `gofaxip-bridge.service`. Add the following content, adjusting paths and flags as needed:

```ini
[Unit]
Description=GoFaxIP-Bridge Service for FreeSWITCH and HylaFAX
After=network.target

[Service]
Type=simple
User=[USER]
ExecStart=/path/to/binary -path=[LOG_FILE_PATH] -spoolerPath=[SPOOLER_PATH] -logDir=[LOG_DIR] -lokiURL=[LOKI_URL] -lokiUser=[LOKI_USER] -lokiPass=[LOKI_PASS]
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

**Enable and Start the Service:**

```shell
sudo systemctl enable gofaxip-bridge
sudo systemctl start gofaxip-bridge
```

**Check the Service Status:**

```shell
sudo systemctl status gofaxip-bridge
```

## Logs and Monitoring

The application logs are stored in the specified log directory. If configured, Prometheus metrics can be accessed on port 9100. Integration with Loki provides advanced log management capabilities.

## Updating GoFaxIP-Bridge

For updates, pull the latest code from the repository, rebuild the binary, and restart the systemd service.

## Support and Contributions

For support, bug reports, or feature requests, please open an issue in the repository or contact the maintainer.

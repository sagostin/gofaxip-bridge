## Description

This application is used to "bridge" GOfaxIP so that when it receives a fax, it will basically send it back out. We are using this for a work-around to hardware transcoding, and so far it seems to work fairly well.

### 

## Usage

example: `/opt/gofaxip-bridge/gofaxip-bridge -path="/var/log/gofaxip/xferfaxlog" -spoolerPath="/var/spool/hylafax" -logDir="./log" -lokiURL="http://your-loki-server:3100/loki/api/v1/push" -lokiUser="your_loki_username" -lokiPass="your_loki_password"`

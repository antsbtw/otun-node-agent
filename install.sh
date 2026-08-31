#!/bin/bash
set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  OTun Node Agent Installer v2.0.0${NC}"
echo -e "${GREEN}  Multi-Protocol Support${NC}"
echo -e "${GREEN}========================================${NC}"

# 检查 root 权限
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}Please run as root (sudo)${NC}"
    exit 1
fi

# 彻底清理已有环境
echo -e "${YELLOW}Cleaning up existing installation...${NC}"

# 停止服务
systemctl stop otun-agent 2>/dev/null || true
systemctl stop sing-box 2>/dev/null || true
systemctl disable otun-agent 2>/dev/null || true
systemctl disable sing-box 2>/dev/null || true

# 强制终止进程
pkill -9 sing-box 2>/dev/null || true
pkill -9 agent 2>/dev/null || true
sleep 2

# 删除旧的二进制文件
rm -f /usr/local/bin/sing-box 2>/dev/null || true
rm -f /opt/otun-agent/agent 2>/dev/null || true

# 删除旧的配置（保留用户数据）
rm -f /etc/sing-box/config.json 2>/dev/null || true

# 删除旧的 systemd 服务文件
rm -f /etc/systemd/system/otun-agent.service 2>/dev/null || true
rm -f /etc/systemd/system/sing-box.service 2>/dev/null || true
systemctl daemon-reload

echo -e "${GREEN}Cleanup completed${NC}"

# 安装必要依赖
echo -e "${GREEN}Installing dependencies...${NC}"
apt-get update -qq
apt-get install -y -qq git curl

# 解析参数
NODE_API_KEY=""
NODE_ID="node-$(hostname)"
VLESS_PORT=443
MANAGEMENT_MODE="local"
SERVER_IP=""

# 多协议参数
VPN_DOMAIN=""
VMESS_PORT=0
TROJAN_PORT=0
HYSTERIA2_PORT=0
TUIC_PORT=0

# TLS 证书参数 (Base64 编码)
CERT_CONTENT=""
KEY_CONTENT=""

# 默认值
API_URL="https://otun-manager.situstechnologies.com"

while [[ $# -gt 0 ]]; do
    case $1 in
        --api-key) NODE_API_KEY="$2"; shift 2 ;;
        --node-id) NODE_ID="$2"; shift 2 ;;
        --api-url) API_URL="$2"; shift 2 ;;
        --vless-port) VLESS_PORT="$2"; shift 2 ;;
        --management-mode) MANAGEMENT_MODE="$2"; shift 2 ;;
        --server-ip) SERVER_IP="$2"; shift 2 ;;
        # 多协议参数
        --vpn-domain) VPN_DOMAIN="$2"; shift 2 ;;
        --vmess-port) VMESS_PORT="$2"; shift 2 ;;
        --trojan-port) TROJAN_PORT="$2"; shift 2 ;;
        --hysteria2-port) HYSTERIA2_PORT="$2"; shift 2 ;;
        --tuic-port) TUIC_PORT="$2"; shift 2 ;;
        # TLS 证书参数
        --cert-content) CERT_CONTENT="$2"; shift 2 ;;
        --key-content) KEY_CONTENT="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

if [ -z "$NODE_API_KEY" ]; then
    echo -e "${RED}Error: --api-key is required${NC}"
    echo "Usage: $0 --api-key <key> [--node-id <id>] [--vless-port <port>] [--management-mode local|remote|hybrid] [--server-ip <ip>]"
    echo ""
    echo "Multi-protocol options:"
    echo "  --vpn-domain <domain>       VPN domain for TLS protocols"
    echo "  --vmess-port <port>         VMess+TLS port (default: disabled)"
    echo "  --trojan-port <port>        Trojan port (default: disabled)"
    echo "  --hysteria2-port <port>     Hysteria2 port (default: disabled)"
    echo "  --tuic-port <port>          TUIC port (default: disabled)"
    echo "  --cert-content <base64>     TLS certificate (base64 encoded)"
    echo "  --key-content <base64>      TLS private key (base64 encoded)"
    exit 1
fi

echo -e "${YELLOW}Node ID: ${NODE_ID}${NC}"
echo -e "${YELLOW}VLESS Port: ${VLESS_PORT}${NC}"
echo -e "${YELLOW}Management Mode: ${MANAGEMENT_MODE}${NC}"

# 显示多协议配置
if [ -n "$VPN_DOMAIN" ]; then
    echo -e "${YELLOW}VPN Domain: ${VPN_DOMAIN}${NC}"
    echo -e "${YELLOW}VMess Port: ${VMESS_PORT}${NC}"
    echo -e "${YELLOW}Trojan Port: ${TROJAN_PORT}${NC}"
    echo -e "${YELLOW}Hysteria2 Port: ${HYSTERIA2_PORT}${NC}"
    echo -e "${YELLOW}TUIC Port: ${TUIC_PORT}${NC}"
fi

# 安装目录
INSTALL_DIR="/opt/otun-agent"
mkdir -p $INSTALL_DIR
cd $INSTALL_DIR

# 安装 Go (仅用于编译 agent)
GO_VERSION="1.23.4"
echo -e "${GREEN}Installing Go ${GO_VERSION}...${NC}"
rm -rf /usr/local/go
ARCH=$(uname -m)
case $ARCH in
    x86_64) GO_ARCH="amd64" ;;
    aarch64) GO_ARCH="arm64" ;;
    *) echo -e "${RED}Unsupported architecture: $ARCH${NC}"; exit 1 ;;
esac
curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" -o go.tar.gz
tar -C /usr/local -xzf go.tar.gz
rm go.tar.gz
export PATH=$PATH:/usr/local/go/bin
grep -q '/usr/local/go/bin' /etc/profile || echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
echo -e "${GREEN}Go installed: $(go version)${NC}"

# 下载预编译的 sing-box (已包含 v2ray_api 和 utls 支持)
echo -e "${GREEN}Downloading pre-built sing-box with v2ray_api support...${NC}"

# sing-box 版本和预编译二进制下载地址
SINGBOX_VERSION="1.10.7"

# 确定架构
case $ARCH in
    x86_64) SINGBOX_ARCH="amd64" ;;
    aarch64) SINGBOX_ARCH="arm64" ;;
esac

# 从 GitHub Release 下载预编译二进制文件
# 这个二进制文件由项目维护者预编译，包含 with_v2ray_api,with_utls 标签
SINGBOX_URL="https://github.com/antsbtw/otun-node-agent/releases/download/v${SINGBOX_VERSION}/sing-box-linux-${SINGBOX_ARCH}"

echo -e "${YELLOW}Downloading sing-box v${SINGBOX_VERSION} for ${SINGBOX_ARCH}...${NC}"
if ! curl -fsSL "$SINGBOX_URL" -o /usr/local/bin/sing-box; then
    # 不再回退编译上游 SagerNet：上游没有自研 fork 的扩展（如 experimental.hot_reload），
    # 编出来的二进制与本项目下发的配置不是同一份契约，装上去只会换一种坏法。
    # 宁可装机失败（可见、可重试），也不要留一台"起得来但行为不对"的节点。
    echo -e "${RED}Failed to download sing-box from ${SINGBOX_URL}${NC}"
    echo -e "${RED}Aborting install. Do NOT fall back to upstream SagerNet source:${NC}"
    echo -e "${RED}  it lacks this project's fork extensions and yields an incompatible binary.${NC}"
    echo -e "${YELLOW}Fix the release asset (tag v${SINGBOX_VERSION}, ${SINGBOX_ARCH}) and re-run.${NC}"
    exit 1
fi

chmod +x /usr/local/bin/sing-box
setcap cap_net_bind_service=+ep /usr/local/bin/sing-box

# 验证安装
if ! sing-box version > /dev/null 2>&1; then
    echo -e "${RED}sing-box installation verification failed${NC}"
    exit 1
fi
echo -e "${GREEN}sing-box installed: $(sing-box version | head -1)${NC}"

cd $INSTALL_DIR

# 下载预编译的 agent
echo -e "${GREEN}Downloading OTun Node Agent...${NC}"

# 确定架构
case $ARCH in
    x86_64) AGENT_ARCH="amd64" ;;
    aarch64) AGENT_ARCH="arm64" ;;
esac

# 从 GitHub Release 下载预编译的 agent
AGENT_URL="https://github.com/antsbtw/otun-node-agent/releases/download/latest/agent-linux-${AGENT_ARCH}"

echo -e "${YELLOW}Downloading agent for ${AGENT_ARCH}...${NC}"
if curl -fsSL "$AGENT_URL" -o $INSTALL_DIR/agent; then
    chmod +x $INSTALL_DIR/agent
    echo -e "${GREEN}Agent downloaded successfully${NC}"
else
    echo -e "${YELLOW}Download failed, falling back to source compilation...${NC}"

    # 备用方案：从源码编译
    if [ -d "repo" ]; then
        cd repo
        git fetch origin
        git reset --hard origin/main
    else
        git clone https://github.com/antsbtw/otun-node-agent.git repo
        cd repo
    fi

    echo -e "${GREEN}Building agent from source...${NC}"
    if ! go build -o $INSTALL_DIR/agent ./cmd/agent; then
        echo -e "${RED}Failed to build agent${NC}"
        exit 1
    fi
    cd $INSTALL_DIR
fi

if [ ! -f "$INSTALL_DIR/agent" ]; then
    echo -e "${RED}Agent binary not found${NC}"
    exit 1
fi
echo -e "${GREEN}Agent ready${NC}"

# 创建数据目录
mkdir -p $INSTALL_DIR/data
mkdir -p $INSTALL_DIR/data/certs
mkdir -p /etc/sing-box

# 保存 TLS 证书 (如果提供)
if [ -n "$CERT_CONTENT" ] && [ -n "$KEY_CONTENT" ]; then
    echo -e "${GREEN}Saving TLS certificates...${NC}"
    echo "$CERT_CONTENT" | base64 -d > $INSTALL_DIR/data/certs/cert.pem
    echo "$KEY_CONTENT" | base64 -d > $INSTALL_DIR/data/certs/key.pem
    chmod 644 $INSTALL_DIR/data/certs/cert.pem
    chmod 600 $INSTALL_DIR/data/certs/key.pem
    echo -e "${GREEN}TLS certificates saved${NC}"
fi

# 创建初始配置
cat > /etc/sing-box/config.json << 'CONF'
{
  "log": {"level": "info", "timestamp": true},
  "inbounds": [],
  "outbounds": [{"type": "direct", "tag": "direct"}]
}
CONF

# 构建环境变量
ENV_VARS="Environment=\"NODE_API_KEY=$NODE_API_KEY\"
Environment=\"NODE_ID=$NODE_ID\"
Environment=\"VLESS_PORT=$VLESS_PORT\"
Environment=\"OTUN_API_URL=$API_URL\"
Environment=\"MANAGEMENT_MODE=$MANAGEMENT_MODE\"
Environment=\"SERVER_IP=$SERVER_IP\""

# 添加多协议环境变量
if [ -n "$VPN_DOMAIN" ]; then
    ENV_VARS="$ENV_VARS
Environment=\"VPN_DOMAIN=$VPN_DOMAIN\""
fi

if [ "$VMESS_PORT" -gt 0 ]; then
    ENV_VARS="$ENV_VARS
Environment=\"VMESS_PORT=$VMESS_PORT\""
fi

if [ "$TROJAN_PORT" -gt 0 ]; then
    ENV_VARS="$ENV_VARS
Environment=\"TROJAN_PORT=$TROJAN_PORT\""
fi

if [ "$HYSTERIA2_PORT" -gt 0 ]; then
    ENV_VARS="$ENV_VARS
Environment=\"HYSTERIA2_PORT=$HYSTERIA2_PORT\""
fi

if [ "$TUIC_PORT" -gt 0 ]; then
    ENV_VARS="$ENV_VARS
Environment=\"TUIC_PORT=$TUIC_PORT\""
fi

# 如果有证书，添加证书路径
if [ -f "$INSTALL_DIR/data/certs/cert.pem" ]; then
    ENV_VARS="$ENV_VARS
Environment=\"TLS_CERT_PATH=$INSTALL_DIR/data/certs/cert.pem\"
Environment=\"TLS_KEY_PATH=$INSTALL_DIR/data/certs/key.pem\""
fi

# 创建 systemd 服务
cat > /etc/systemd/system/otun-agent.service << SYSTEMD
[Unit]
Description=OTun Node Agent
After=network.target

[Service]
Type=simple
WorkingDirectory=$INSTALL_DIR
$ENV_VARS
ExecStart=$INSTALL_DIR/agent
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
SYSTEMD

# 启动服务
systemctl daemon-reload
systemctl enable otun-agent
systemctl start otun-agent

# 等待 agent 启动并生成 secrets
echo -e "${GREEN}Waiting for agent to start...${NC}"
sleep 5

# 确保 secrets.json 存在
for i in {1..10}; do
    if [ -f "$INSTALL_DIR/data/secrets.json" ]; then
        break
    fi
    sleep 1
done

# 创建管理命令
cat > /usr/local/bin/otun << 'CMD'
#!/bin/bash
case "$1" in
    start)   systemctl start otun-agent ;;
    stop)    systemctl stop otun-agent ;;
    restart) systemctl restart otun-agent ;;
    status)  systemctl status otun-agent ;;
    logs)    journalctl -u otun-agent -f ;;
    *)       echo "Usage: otun {start|stop|restart|status|logs}" ;;
esac
CMD
chmod +x /usr/local/bin/otun

# 生成 secrets.json (如果不存在)
if [ ! -f "$INSTALL_DIR/data/secrets.json" ]; then
    echo -e "${YELLOW}Generating secrets...${NC}"
    # 等待 agent 生成 secrets
    sleep 3
fi

# 读取并增强 secrets.json 以包含多协议端口
if [ -f "$INSTALL_DIR/data/secrets.json" ]; then
    # 读取现有的 secrets
    SECRETS=$(cat $INSTALL_DIR/data/secrets.json)

    # 使用 Python 添加多协议端口 (如果有 Python)
    if command -v python3 &> /dev/null; then
        python3 << PYTHON
import json

try:
    with open('$INSTALL_DIR/data/secrets.json', 'r') as f:
        secrets = json.load(f)
except:
    secrets = {}

# 添加多协议端口
secrets['vmess_port'] = $VMESS_PORT
secrets['trojan_port'] = $TROJAN_PORT
secrets['hysteria2_port'] = $HYSTERIA2_PORT
secrets['tuic_port'] = $TUIC_PORT

with open('$INSTALL_DIR/data/secrets.json', 'w') as f:
    json.dump(secrets, f, indent=2)
PYTHON
    fi
fi

# ---------------------------------------------------------------------------
# 数据面验收：agent 活着 != 用户能连。
#
# 2026-08-31 事故：sing-box 因配置被拒而崩溃重启循环，但 agent 进程好好的、8080 也应答，
# 于是装机"成功"、DB 标 active，实际 443/SS 全拒 —— 两台生产机分别烂了 1 天和 23 天。
# 装机阶段就必须把这种机器判为失败：装机失败是可见、可重试的；静默交付一台连不上的机器不是。
#
# /health: 200 = agent + sing-box 都在跑；503 = agent 在但 sing-box 没起来（数据面死的）。
#
# ★ 只对 local 模式（OBox 托管机）生效。remote 模式（vpn/otun 标准出口节点）跳过：
#   标准节点的用户集要等 agent 向 manager 拉一轮才有，装机当刻 sing-box 未必已就绪，
#   这个空窗期没有实测过；而标准节点线上承载着数百个用户，宁可不加这道闸，
#   也不能因为一个未验证的判据把本来正常的装机判失败。
#   标准节点的同类保护另行评估（需先实测 remote 首装的 health 时序）。
# ---------------------------------------------------------------------------
if [ "$MANAGEMENT_MODE" != "local" ]; then
    echo -e "${YELLOW}Skipping data-plane gate (management mode: ${MANAGEMENT_MODE}; gate applies to local/OBox only)${NC}"
else
echo ""
echo -e "${GREEN}Verifying data plane...${NC}"

HEALTH_OK=0
for i in $(seq 1 6); do
    HEALTH_CODE=$(curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/health 2>/dev/null || echo "000")
    echo -e "  attempt ${i}/6: health=${HEALTH_CODE}"
    if [ "$HEALTH_CODE" = "200" ]; then
        HEALTH_OK=1
        break
    fi
    sleep 5
done

if [ "$HEALTH_OK" != "1" ]; then
    echo -e "${RED}========================================${NC}"
    echo -e "${RED}  INSTALL FAILED: data plane is down${NC}"
    echo -e "${RED}========================================${NC}"
    echo -e "${RED}Agent responds but sing-box is not running -- users could NOT connect.${NC}"
    echo -e "${YELLOW}sing-box config check:${NC}"
    /usr/local/bin/sing-box check -c /etc/sing-box/config.json 2>&1 | tail -5
    echo -e "${YELLOW}Recent agent logs:${NC}"
    journalctl -u otun-agent --no-pager -n 30 2>/dev/null | grep -iE 'sing-box|FATAL' | tail -5
    exit 1
fi

echo -e "${GREEN}Data plane OK (sing-box running)${NC}"
fi

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  Installation Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo -e "Node ID: ${YELLOW}$NODE_ID${NC}"
echo -e "Config:  ${YELLOW}/etc/sing-box/config.json${NC}"
echo -e "Data:    ${YELLOW}$INSTALL_DIR/data${NC}"

if [ -n "$VPN_DOMAIN" ]; then
    echo ""
    echo -e "${GREEN}Multi-Protocol Configuration:${NC}"
    echo -e "  VPN Domain:    ${YELLOW}$VPN_DOMAIN${NC}"
    echo -e "  VLESS Port:    ${YELLOW}$VLESS_PORT${NC}"
    echo -e "  VMess Port:    ${YELLOW}$VMESS_PORT${NC}"
    echo -e "  Trojan Port:   ${YELLOW}$TROJAN_PORT${NC}"
    echo -e "  Hysteria2 Port: ${YELLOW}$HYSTERIA2_PORT${NC}"
    echo -e "  TUIC Port:     ${YELLOW}$TUIC_PORT${NC}"
fi

echo ""
echo -e "Commands:"
echo -e "  ${YELLOW}otun status${NC}  - Check service status"
echo -e "  ${YELLOW}otun logs${NC}    - View logs"
echo -e "  ${YELLOW}otun restart${NC} - Restart service"
echo ""
echo -e "${GREEN}Secrets generated:${NC}"
cat $INSTALL_DIR/data/secrets.json 2>/dev/null || echo "Will be generated on first run"

# 节点性能观察手册(CPU / 带宽 / 用户数)

> 目的:在 VPN 节点上持续采样 **CPU、带宽、用户数** 三个值,
> 未雨绸缪 —— 卡顿投诉来时能拿时间点对照这三者的关系。
>
> 核心结论(已实测验证,2026-06-18):
> **VLESS / sing-box 转发在十几 Mbps 量级下几乎不吃 CPU。**
> 节点真正会先撞墙的是 **带宽配额** 和 **reload churn**,不是 CPU 算力。
> 所以"忙不忙"看带宽,不要看连接数。

---

## 0. 三个值各是什么、准不准(重要,别看错)

| 值 | 日志字段 | 来源 | 准确度 |
|----|---------|------|--------|
| CPU 占用 | `cpu` `load` | `/proc/stat` `/proc/loadavg` | ✅ 准(3 秒区间均值) |
| 带宽 | `rx` `tx` | `/proc/net/dev` | ✅ 准(**真正要盯的瓶颈**) |
| 用户数 | `srcips` | `ss` 数不同来源公网 IP | ⚠️ **只是下限**,非真实用户数 |
| (连接形态) | `vless` `active` | `ss` | 参考(连接数 ≠ 用户数) |
| reload 探针 | `sb_etime` | sing-box 进程运行秒数 | ✅ 突然归零 = 发生过 reload 重启 |

**为什么 `srcips` 只是下限:** 一个公网 IP 后面可能是 NAT / CGNAT 出口,
挂着一个家庭、一栋楼甚至整个运营商网关的很多真人。
所以 **真实用户数 ≥ srcips**。要数准真实用户,必须按 UUID(见第 5 节,待补)。

---

## 1. 采样脚本

脚本:`~/node_sample.sh`(放在 home,**不要放 /tmp**,/tmp 重启会被清)

```bash
#!/bin/bash
# 节点性能采样:每行一条,逗号分隔。日志自动截断保留最近 5000 行。
LOG="$HOME/node_perf.log"
PID=$(pgrep -x sing-box | head -1)
NIC=$(ip -o link show | awk -F': ' '/ens|eth|enp/{print $2; exit}')

R1=$(awk -v n="$NIC:" '$1==n{print $2}' /proc/net/dev)
T1=$(awk -v n="$NIC:" '$1==n{print $10}' /proc/net/dev)
C1=$(awk '/^cpu /{for(i=2;i<=8;i++)s+=$i; print s, $5}' /proc/stat)
sleep 3
R2=$(awk -v n="$NIC:" '$1==n{print $2}' /proc/net/dev)
T2=$(awk -v n="$NIC:" '$1==n{print $10}' /proc/net/dev)
C2=$(awk '/^cpu /{for(i=2;i<=8;i++)s+=$i; print s, $5}' /proc/stat)

read ct1 ci1 <<<"$C1"; read ct2 ci2 <<<"$C2"
dt=$((ct2-ct1)); di=$((ci2-ci1))
cpu=$(awk -v dt=$dt -v di=$di 'BEGIN{if(dt>0)printf "%.1f",(1-di/dt)*100; else print 0}')

rx=$(( (R2-R1)/3 )); tx=$(( (T2-T1)/3 ))
load=$(awk '{print $1}' /proc/loadavg)
vless=$(ss -tn 2>/dev/null | grep ':443 ' | grep -c ESTAB)
srcips=$(ss -tn 2>/dev/null | grep ':443 ' | grep ESTAB | awk '{print $5}' | sed 's/:[0-9]*$//' | sort -u | wc -l)
active=$(ss -tn 2>/dev/null | grep ':443 ' | grep ESTAB | awk '$2!=0||$3!=0' | wc -l)
uptime=$(ps -o etimes= -p "$PID" 2>/dev/null | tr -d ' ')

echo "$(date -u +%FT%TZ),cpu=${cpu}%,load=${load},rx=$((rx/1024))KB/s,tx=$((tx/1024))KB/s,vless=${vless},srcips=${srcips},active=${active},sb_etime=${uptime}s" >> "$LOG"

# 日志自动截断:只保留最近 5000 行(约 3.5 天 @1min),不用手动清
if [ "$(wc -l < "$LOG")" -gt 5000 ]; then tail -n 5000 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"; fi
```

> 说明:本版把写日志 + 自动截断收进脚本内部,所以**启动命令不再带重定向**。
> 旧版有个 bug —— `sb_etime` 会输出两行(误把 PID 当时间),本版已修(只用 `ps etimes`)。

---

## 2. 启动 / 停止 / 查看

```bash
# 启动(每 60 秒一行;脚本自己写日志,命令不带 >> )
nohup bash -c 'while true; do ~/node_sample.sh; sleep 57; done' >/dev/null 2>&1 &

# 看最近 20 行
tail -20 ~/node_perf.log

# 实时跟随
tail -f ~/node_perf.log

# 确认采样进程还在
pgrep -af node_sample

# 停止
pkill -f node_sample
```

⚠️ **这是临时后台进程,节点重启后不自救。** 想长期开机自启需做成
systemd timer / cron —— 那属于在**生产节点新增常驻服务**,
按"VPS 是生产、默认只读"的原则,**需明确授权后再做**,本手册不默认启用。

---

## 3. 怎么读这三个值的关系(日常观察)

一行长这样:
```
2026-06-18T10:19:13Z,cpu=0.2%,load=0.00,rx=374KB/s,tx=356KB/s,vless=108,srcips=32,active=6,sb_etime=1700s
```

**例行只需扫这几列:**

1. **带宽(rx/tx)—— 第一优先**
   持续接近 AWS 实例带宽配额 = 真瓶颈。
   t 系列实例有突发额度,长时间高位会被限速 → 表现为用户卡顿。
   这是三个值里**唯一真正会先撞墙的**。

2. **CPU(cpu/load)—— 几乎不用担心**
   实测带宽十几 Mbps 时仍 0.x%。
   只有当带宽冲到几百 Mbps~Gbps 时,CPU(尤其 softirq)才可能见顶。
   `load` 持续 > 核数(本机 2 核)才需警惕。

3. **用户数(srcips)—— 当下限趋势看**
   只看**变化趋势**(涨/跌),别当精确人数。
   要精确人数走第 5 节。

4. **sb_etime —— churn 探针**
   正常应**单调递增**。若某行突然变小(归零),
   说明 sing-box 被重启过 = 发生 reload 断流。
   这是本团队的老对手,用此列抓现行,和卡顿时段对照。

**判断口诀:**
> 卡顿 + 带宽高位 → 带宽配额瓶颈(换/加实例、限速、负载均衡)
> 卡顿 + 带宽不高 + sb_etime 归零过 → reload churn(查 version 抖动)
> 卡顿 + 带宽不高 + sb_etime 正常 + CPU 低 → 不在本机,查上游/出口/GFW

---

## 4. 推荐观察节奏

| 频率 | 做什么 |
|------|--------|
| 每天 1 次 | `tail -50 ~/node_perf.log`,扫高峰段(北京 20:00-23:00 / UTC 12:00-15:00)带宽峰值 |
| 卡顿投诉时 | 拿投诉时间点 → 日志对应行 → 按第 3 节口诀定位 |
| 每周 | 看带宽峰值是否逼近实例配额;`grep` 检查 sb_etime 有没有归零过 |

一行命令抓"本日志里有没有发生过 reload(sb_etime 回退)":
```bash
awk -F'sb_etime=' '{v=$2+0; if(prev && v<prev) print "RELOAD detected at:", $0; prev=v}' ~/node_perf.log
```

---

## 5. 【待补】真实用户数(按 UUID)

当前 `srcips` 只是下限。要真实用户数,数据源有两个,**节点本地拿不到**:

- 节点开了 `v2ray_api` stats(`127.0.0.1:10085`,gRPC,按 UUID 统计),
  但节点上**没有 grpcurl 且不装软件**,本地查不了。
- 正确做法:从 **otun-manager** 取 —— 它消费 stats 做计费,手里有全节点
  按 UUID 的活跃/流量数据。接口待确认(查 otun-manager 的 OPS_GUIDE / 代码)。

> 已知偏差:otun-manager 的 v2ray_api `stats.users` 集合在构造期定死,
> 热更(hot-reload)进来的新用户可能不在 stats 里 → 用 stats 数活跃用户
> 会**漏掉热更新增用户**。接入时需标注此偏差。

**TODO:** 在 otun-manager 找"按节点活跃用户数"现成接口,作为第二维补进观察流程。

---

## 6. 适用范围

- 已在节点验证:`172.26.1.85`(标准节点)、`172.26.10.180`(东京)。
- 两台 config 一致,脚本通用。
- 端口约定:VLESS 443 / VMess 8443 / Trojan 8444 / SS(节点各异)/ HY2 8445/udp / TUIC 8446/udp。
  大陆环境实测 HY2/TUIC 不可用,真实流量基本全在 VLESS 443。




连接数观察
echo "===== 各协议 inbound 当前连接数 ====="
echo "VLESS(443):     $(sudo ss -tnp 2>/dev/null | grep sing-box | grep ':443 '   | grep -c ESTAB)"
echo "VMess(8443):    $(sudo ss -tnp 2>/dev/null | grep sing-box | grep ':8443 '  | grep -c ESTAB)"
echo "Trojan(8444):   $(sudo ss -tnp 2>/dev/null | grep sing-box | grep ':8444 '  | grep -c ESTAB)"
echo "SS(看节点ss端口): $(sudo ss -tnp 2>/dev/null | grep sing-box | grep -cE ':(24238|47209) ')"
echo "HY2(8445/udp):  $(sudo ss -unp 2>/dev/null | grep sing-box | grep -c ':8445 ')"
echo "TUIC(8446/udp): $(sudo ss -unp 2>/dev/null | grep sing-box | grep -c ':8446 ')"

echo "===== VLESS(443) established 连接的来源 IP 分布(前20) ====="
sudo ss -tnp 2>/dev/null | grep sing-box | grep ':443 ' | grep ESTAB \
  | awk '{print $5}' | sed 's/:[0-9]*$//' | sed 's/^\[//;s/\]$//' \
  | sort | uniq -c | sort -rn | head -20

echo "===== 总连接 + sing-box 进程状态 ====="
sudo ss -s | head -3
date -u; ps -eo pid,etimes,cmd | grep -i "[s]ing-box"

热重启观察

sudo journalctl -u otun-agent --since "8 hours ago" --no-pager | grep -iE "hot-reload|→ reload|user_version|param_version|Only user|FALL BACK|changed" | tail -40

性能观察

echo "===== CPU 核数 + 负载 ====="
nproc
cat /proc/loadavg
# loadavg 前三个数 vs nproc:超过核数=CPU 排队

echo "===== sing-box 进程 CPU/内存(瞬时) ====="
top -b -n1 -p 2667 | tail -4
# 关注 %CPU(单核满载=100%,多核可超100) 和 %MEM

echo "===== sing-box 进程 CPU/内存(2秒采样,更准) ====="
pidstat -p 2667 1 2 2>/dev/null || (echo "pidstat 缺,用 top 替代"; top -b -d1 -n2 -p 2667 | grep sing-box)

echo "===== 整机 CPU 使用率拆分(user/sys/iowait/softirq) ====="
mpstat 1 2 2>/dev/null || vmstat 1 3

echo "===== 内存总览 ====="
free -m

echo "===== sing-box 打开的文件描述符数 vs 上限 ====="
sudo ls /proc/2667/fd 2>/dev/null | wc -l
cat /proc/2667/limits | grep -i "open files"
# fd 数接近 limit 就是瓶颈(每连接占 fd)

echo "===== 网卡流量速率(2秒) ====="
cat /proc/net/dev | grep -E "eth0|ens|enp"
sleep 2
echo "--- 2s 后 ---"
cat /proc/net/dev | grep -E "eth0|ens|enp"
# 两次 RX/TX bytes 差 /2 = 字节/秒

echo "===== softirq(网络中断,高并发转发关键指标) ====="
cat /proc/softirqs | grep -E "NET_RX|NET_TX"


观察流量

echo "===== 抓 10 秒网卡速率(看到底有没有流量) ====="
R1=$(cat /proc/net/dev | awk '/ens5:/{print $2}')
T1=$(cat /proc/net/dev | awk '/ens5:/{print $10}')
sleep 10
R2=$(cat /proc/net/dev | awk '/ens5:/{print $2}')
T2=$(cat /proc/net/dev | awk '/ens5:/{print $10}')
echo "RX: $(( (R2-R1)/10 )) 字节/秒  ($(( (R2-R1)/10/1024 )) KB/s)"
echo "TX: $(( (T2-T1)/10 )) 字节/秒  ($(( (T2-T1)/10/1024 )) KB/s)"

echo "===== VLESS(443) 连接的 Send-Q/Recv-Q(队列非0=正在传数据) ====="
sudo ss -tn 2>/dev/null | grep ':443 ' | grep ESTAB | awk '{print $2, $3, $5}' | sort | head -40
# 第1列Recv-Q 第2列Send-Q:都=0 说明此刻没在传;非0=正在收发

echo "===== 这 81 条连接里,Send-Q 或 Recv-Q 非 0 的有几条(真正活跃的) ====="
sudo ss -tn 2>/dev/null | grep ':443 ' | grep ESTAB | awk '$2!=0 || $3!=0' | wc -l

echo "===== 按来源 IP 再统计一次连接数 ====="
sudo ss -tn 2>/dev/null | grep ':443 ' | grep ESTAB \
  | awk '{print $4}' | sed 's/:[0-9]*$//' | sort | uniq -c | sort -rn | head


echo "=== 连续 30 次握手,统计抖动(北京时间高峰现场) ==="
for ip in 13.229.211.231 13.231.152.206; do
  echo "--- $ip ---"
  slow=0; total=30
  for i in $(seq 1 $total); do
    c=$(curl -o /dev/null -s -w "%{time_connect}" -m 8 https://$ip:443 -k 2>/dev/null || echo 99)
    # 超过 0.5 秒算抖动
    awk "BEGIN{exit !($c>0.5)}" && { slow=$((slow+1)); echo "  慢: ${c}s"; }
    sleep 0.5
  done
  echo "  >>> $ip: $total 次里 $slow 次握手 >0.5s(抖动率)"
done



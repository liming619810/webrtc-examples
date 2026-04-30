下面给你画一张**WebRTC 底层协议流程图**，专门讲清楚：
**ICE → DTLS → SRTP** 这三步是怎么串起来的，并且和你上面的 Go 代码完全对应。

我用**程序员最容易看懂的层级 + 时序流程**两种方式画，不搞玄学。

---

# 一、WebRTC 整体协议栈（层级图）
```
应用层
  ↓
【信令】SDP Offer/Answer（你代码里的 base64 交换）
  ↓
【传输】UDP（WebRTC 几乎只用 UDP）
  ↓
┌─────────────────────────────────────┐
│ ICE （打洞、找可用链路、选最优路径）  │  <-- 你的 iceConnectedCtx 等的就是它
├─────────────────────────────────────┤
│ DTLS （加密握手，建立安全通道）      │
├─────────────────────────────────────┤
│ SRTP （音视频加密传输）             │
└─────────────────────────────────────┘
  ↓
音视频 RTP 包（H.264/Opus）
```

一句话总结：
**先 ICE 打通网络 → 再 DTLS 加密 → 最后用 SRTP 发加密的音视频。**

---

# 二、完整时序流程图（最关键）
```
【1. 信令阶段】
浏览器 Offer  → 粘贴到 Go 程序
Go 设置 SetRemoteDescription(offer)

【2. 本地准备】
Go 创建 Answer
Go 设置 SetLocalDescription(answer)
Go 开始收集 ICE Candidate（IP+端口）
→ 把 Answer + ICE 信息一起返回浏览器

【3. ICE 阶段（网络连通）】
浏览器 ↔ Go 互相交换 ICE 地址
双方互相 PING 连通性检查
→ 找到一条能通的 UDP 路径

【此时触发】
OnICEConnectionStateChange → Connected
→ iceConnectedCtxCancel()
→ 音视频发送协程解除阻塞，准备发数据

【4. DTLS 握手（加密）】
ICE 通了之后，自动开始 DTLS 握手
双方交换证书、协商加密算法
→ 建立安全加密通道

【5. 协商 SRTP 密钥】
DTLS 握手过程中，自动导出 SRTP 密钥
→ 音视频要用的加密密钥就在这里生成

【6. 正式发 SRTP 音视频】
Go 发送 H.264/Opus 数据
→ 封装成 RTP
→ 用 SRTP 加密
→ 通过 UDP 发出去

浏览器接收：
UDP → SRTP 解密 → RTP → 解码播放
```

---

# 三、ICE / DTLS / SRTP 分别干什么？

## 1. ICE（Interactive Connectivity Establishment）
**作用：找到一条浏览器 ↔ Go 程序能通的网络路径。**

- 收集本地 IP、内网 IP、外网 IP（通过 STUN）
- 尝试各种端口、各种链路
- 选出**最优、最快、能通**的一条 UDP 通道
- 成功后状态变为 `ICEConnectionStateConnected`

你代码里的：
```go
<-iceConnectedCtx.Done()
```
**等的就是 ICE 连通这一刻。**

---

## 2. DTLS（Datagram TLS）
**作用：在 UDP 上做加密握手，类似 HTTPS 但面向数据包。**

- 不依赖 TCP
- 双向认证
- 协商加密套件
- 生成密钥材料

**DTLS 成功之后，通道才安全。**

---

## 3. SRTP（Secure Real-time Transport Protocol）
**作用：专门给音视频 RTP 包加密。**

- 密钥来自 DTLS 握手
- 对音视频数据加密、防篡改
- 保证实时性，不搞重传

你代码里：
```go
videoTrack.WriteSample(...)
```
最终发出去的就是 **SRTP 加密包**，不是裸 RTP。

---

# 四、和你代码的对应关系（超级重要）
```
iceConnectedCtx 等待的是 ICE 连通
↓
ICE 连通 → DTLS 自动开始
↓
DTLS 完成 → SRTP 密钥就绪
↓
然后才敢 WriteSample 发音视频
```

**顺序绝对不能乱：**
- ICE 不通 → 发啥都丢
- DTLS 不成 → 发了浏览器也解不开
- SRTP 没密钥 → 音视频是加密乱码

---

# 五、极简记忆版（面试也能用）
1. **ICE**：找路（网络连通）
2. **DTLS**：上锁（加密握手）
3. **SRTP**：送信（加密音视频）

---


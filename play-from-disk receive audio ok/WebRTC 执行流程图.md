好，我给你画一张**极简、一步不漏、程序员一眼看懂**的 WebRTC 执行流程图，完全对应你这段代码。

# 整个程序执行流程（按代码顺序）

## 1. 启动检查
```
检查 output.h264 / output.ogg 是否存在
```

## 2. 创建 WebRTC 连接
```
创建 peerConnection
配置 STUN 服务器
```

## 3. 创建两个“等待信号”
```go
iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())
```
作用：
- 造一个**开关**
- 开关没开 → 音视频协程**卡住不动**
- 开关打开 → 音视频开始发送

## 4. 添加音视频轨道
```
创建 Video Track / Audio Track
加入 peerConnection
启动协程读取 RTCP（NACK 重传用）
```

## 5. 启动音视频发送协程（关键点）
```go
go func() {
    <-iceConnectedCtx.Done()   // 阻塞！等连接成功！

    // 连接成功后才开始循环读文件、发帧
}()
```
**这里不等待，发的数据全丢。**

## 6. 注册连接状态回调
```go
peerConnection.OnICEConnectionStateChange(func(...) {
    if state == Connected {
        iceConnectedCtxCancel()  // 打开开关！
    }
})
```
连接成功 → 调用 cancel() → 第5步的阻塞解除 → 开始发音视频。

## 7. 等待用户粘贴 Offer
```
控制台等待输入 base64 offer
```

## 8. WebRTC 信令交换
```
SetRemoteDescription(offer)
CreateAnswer
SetLocalDescription(answer)
等待 ICE 收集完成
输出 base64 answer 给浏览器
```

## 9. 浏览器开始连接
浏览器收到 answer 后：
```
开始 ICE 连通
连接成功 → 触发 OnICEConnectionStateChange → Connected
```

## 10. 真正开始发送数据
```
iceConnectedCtxCancel() 被调用
<-iceConnectedCtx.Done() 结束阻塞
音视频协程开始循环读文件 → 发送帧
```

## 11. 播放完成
```
读到文件 EOF → 打印提示 → 退出程序
```

---

# 一张图看懂核心逻辑（最关键）
```
程序启动
   ↓
创建 ctx（开关关闭）
   ↓
启动音视频协程 → 阻塞等待开关
   ↓
信令交换（offer/answer）
   ↓
ICE 连通
   ↓
调用 cancel() → 开关打开
   ↓
音视频协程解除阻塞 → 开始发流
```

---

# 回到你最初的三个问题，用图再总结一次

1. **为什么需要 iceConnectedCtx、iceConnectedCtxCancel？**
   ```
   不让数据在连接没建好时发送
   避免丢包、黑屏、没声音
   ```

2. **为什么用 context.Background()？**
   ```
   这是全局根上下文
   代表“整个程序生命周期”
   不是某个请求、不是某个函数
   ```

3. **能不能挂在 HTTP 的 ctx 上？**
   ```
   绝对不能！
   HTTP 请求是短的
   WebRTC 是长连接
   HTTP 结束会把 WebRTC 一起取消，直接断连
   ```

---

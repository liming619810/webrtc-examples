# webrtc-exampls
golang webRTC examples

# 概要

```
这是从 https://github.com/pion/webrtc.git copy 下来的项目
主要是实际的项目:
实现实体设备(门禁机)的实时双向通话功能

'save-to-disk (opus-ogg-ffmpeg ok)' 目前最可行的方案
  接受到麦克风的音频流 --> 保存为 内存中的小文件 --> ffmpeg 转码 G711 --> webscoket(设备)
  生产环境可用的

```

## 1.设备-->web页面
```
play-from-disk-h264
接入设备 webscoket音频流-->转码为opus -->web页面
```
## 2.web页面-->设备

### success:
####  a 'save-to-disk (opus-ogg-ffmpeg ok)' 目前最可行的方案
```
  接受到麦克风的音频流 --> 保存为 内存中的小文件 --> ffmpeg 转码 G711 --> webscoket(设备)
  生产环境可用的
```  
#### b. 'save-to-disk (ogg to memory to ffmpeg to G711)'/
```
  接受到麦克风的音频流 --> 保存为 磁盘上的小文件 --> ffmpeg 转码 G711 --> webscoket(设备)
```
### failer:
#### 'save-to-disk (ogg to ffmpeg)'
```
接受到麦克风的音频流 --> 1 存储为 ogg文件(opus格式) -->2 解析opus流--> 3转码为G711 -->4 webscoket(设备)
到第2步失败了， opus流解析失败
报错：Opus解码失败:unsupported configuration mode: 2 Payload[0]:FC payload长度:161
```
#### 'save-to-disk(opus-->G711)'/ 
```
 接受到麦克风的音频流 --> 1 存储为 ogg文件(opus格式) -->2 解析opus流--> 3 通过ffmpeg 转码为G711 -->4 webscoket(设备)
opus包的解析库 从"github.com/pion/opus" -->  "github.com/hraban/opus" 
opus包能解析了，但是 转码为G711后，推送webscoket给设备后，还是无效， 设备不识别
```
#### 'save-to-disk (opus save to ogg)'/
```
接受到麦克风的音频流 --> 1 存储为 ogg文件(opus格式) （为了判断麦克风的数据是否能够接收到）
 -->2 解析opus流--> 3转码为G711 -->4 webscoket(设备)
到第2步失败了， opus流解析失败
报错：Opus解码失败:unsupported configuration mode: 2 Payload[0]:FC payload长度:161

opus包的解析库 "github.com/pion/opus"
```
#### 'save-to-disk(opus to g711)'/
```
接受到麦克风的音频流 --> 1 存储为 ogg文件(opus格式) -->2 解析opus流--> 3 通过ffmpeg 转码为G711 -->4 webscoket(设备)

opus包的解析库 从"github.com/pion/opus" -->  "github.com/hraban/opus" 
opus包能解析了，但是 转码为G711后，推送webscoket给设备后，还是无效， 设备不识别
```

## 3. send oog file to webscoket
```
  oggfile --> ffmpeg to G711 --> webscoket
  把ogg文件转码成 G711 格式并推送给 webscoket
```  
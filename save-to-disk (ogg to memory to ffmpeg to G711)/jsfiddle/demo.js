/* eslint-env browser */

// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT
// urls: 'stun:stun.l.google.com:19302'
// urls: 'stun:192.168.88.15:3478'
const pc = new RTCPeerConnection({
  iceServers: [
    {
      urls: 'stun:stun.l.google.com:19302'
    }
  ]
})
const log = msg => {
  document.getElementById('logs').innerHTML += msg + '<br>'
  // 同时在控制台打印，方便查看
  console.log(msg)
}

// 新增：检测并打印所有音视频设备信息
log("🔍 开始检测媒体设备...")
navigator.mediaDevices.enumerateDevices()
  .then(devices => {
    const audioInputs = devices.filter(device => device.kind === 'audioinput');
    const videoInputs = devices.filter(device => device.kind === 'videoinput');
    const audioOutputs = devices.filter(device => device.kind === 'audiooutput');

    log(`📢 音频输入设备（麦克风）数量: ${audioInputs.length}`);
    audioInputs.forEach((device, index) => {
      log(`   麦克风${index + 1}: 名称=${device.label || '未知名称'}, ID=${device.deviceId}`);
    });

    log(`🎥 视频输入设备（摄像头）数量: ${videoInputs.length}`);
    videoInputs.forEach((device, index) => {
      log(`   摄像头${index + 1}: 名称=${device.label || '未知名称'}, ID=${device.deviceId}`);
    });

    log(`🔊 音频输出设备（扬声器）数量: ${audioOutputs.length}`);
    audioOutputs.forEach((device, index) => {
      log(`   扬声器${index + 1}: 名称=${device.label || '未知名称'}, ID=${device.deviceId}`);
    });

    if (audioInputs.length === 0) {
      log("⚠️ 警告：未检测到音频输入设备（麦克风）");
    }
    if (videoInputs.length === 0) {
      log("⚠️ 警告：未检测到视频输入设备（摄像头）");
    }

    // 设备检测完成后，继续原有的权限获取逻辑
    log("✅ 初始化完成，开始获取音视频权限...")
    return navigator.mediaDevices.getUserMedia({ video: false, audio: true });

    // return navigator.mediaDevices.getUserMedia({ video: false, audio: {
    //             echoCancellation: true,    // 关键参数：启用回声消除
    //             noiseSuppression: true,    // 噪声抑制
    //             autoGainControl: true,     // 自动增益控制
    //             channelCount: 1            // 单声道
    //         } });
  })
  // .then(stream => {
  //   log("✅ 成功获取音视频流")
  //   document.getElementById('video1').srcObject = stream
  //   stream.getTracks().forEach(track => pc.addTrack(track, stream))
  //   log("✅ 音视频轨道已添加到 PeerConnection")

  //   return pc.createOffer()
  //     .then(d => pc.setLocalDescription(d))
  //     .then(() => log("✅ setLocalDescription 执行成功"))
  //     .catch(err => log("❌ createOffer/setLocalDescription 失败: " + err))
  // }).catch(err => {
  //   log("❌ 获取音视频权限/检测设备失败: " + err)
  // })
 .then(stream => {
    log("✅ 成功获取音视频流")
    document.getElementById('video1').srcObject = stream
    stream.getTracks().forEach(track => pc.addTrack(track, stream))
    log("✅ 音视频轨道已添加到 PeerConnection")

    // --- 核心修改开始 ---
    return pc.createOffer()
      .then(offer => {
        log("📝 原始 Offer SDP 已创建");
        
        // // 1. 禁用 Opus 的带内纠错 (FEC)，这是产生 RED 包的主要原因
        // let modifiedSdp = offer.sdp.replace(/useinbandfec=1/g, "useinbandfec=0");
        
        // // 2. 从 SDP 中完全移除 'red' 和 'ulpfec' 编解码器，强制使用纯 Opus
        // // 这会修改 m=audio 行，只保留 opus, PCMU, PCMA 等，剔除 red
        // const sdpLines = modifiedSdp.split('\r\n');
        // for (let i = 0; i < sdpLines.length; i++) {
        //   if (sdpLines[i].startsWith('m=audio')) {
        //     // 找到音频媒体行，将其中的 red 和 ulpfec 负载类型移除
        //     const mLineParts = sdpLines[i].split(' ');
        //     const filteredParts = mLineParts.filter(part => part !== 'red' && part !== 'ulpfec');
        //     sdpLines[i] = filteredParts.join(' ');
        //     log("🛠️ 已从 m=audio 行中移除 RED/ULPFEC 协议");
        //     break;
        //   }
        // }
        // modifiedSdp = sdpLines.join('\r\n');

        // // 3. 将修改后的 SDP 重新赋值给一个新的 RTCSessionDescription 对象
        // const modifiedOffer = new RTCSessionDescription({
        //   type: 'offer',
        //   sdp: modifiedSdp
        // });

        // log("🔧 SDP 修改完成，准备设置本地描述");
        // // 4. 使用修改后的 Offer 来设置本地描述
        // return pc.setLocalDescription(modifiedOffer);
        return pc.setLocalDescription(offer);
      })
      .then(() => log("✅ setLocalDescription 执行成功"))
      .catch(err => log("❌ createOffer/setLocalDescription 失败: " + err));
    // --- 核心修改结束 ---

  }).catch(err => {
    log("❌ 获取音视频权限/检测设备失败: " + err)
  })

pc.oniceconnectionstatechange = e => {
  log("🔄 ICE 连接状态变化: " + pc.iceConnectionState)
}

// 重点：给 onicecandidate 添加完整日志
pc.onicecandidate = event => {
  log(`🆔 ICE 候选事件触发，event.candidate = ${event.candidate ? '存在' : 'null'}`)
  
  if (event.candidate === null) {
    log("✅ 触发：ICE 收集完成（candidate === null）")
    try {
      const sdpObj = pc.localDescription
      let sdp = sdpObj.sdp.replace("useinbandfec=1", "useinbandfec=0");
      sdpObj.sdp = sdp;
      log("✅ pc.localDescription 内容: " + JSON.stringify(sdpObj))
      const encoded = btoa(JSON.stringify(sdpObj))
      document.getElementById('localSessionDescription').value = encoded
      
      log("✅ SDP 已成功写入 localSessionDescription 输入框")
    } catch (e) {
      log("❌ 编码/写入 SDP 失败: " + e)
    }
  }
}

window.startSession = () => {
  const sd = document.getElementById('remoteSessionDescription').value
  if (sd === '') {
    return alert('Session Description must not be empty')
  }

  try {
    pc.setRemoteDescription(JSON.parse(atob(sd)))
    log("✅ setRemoteDescription 执行成功")
  } catch (e) {
    alert(e)
    log("❌ setRemoteDescription 失败: " + e)
  }
}

window.copySDP = () => {
  const browserSDP = document.getElementById('localSessionDescription')

  browserSDP.focus()
  browserSDP.select()

  try {
    const successful = document.execCommand('copy')
    const msg = successful ? 'successful' : 'unsuccessful'
    log('Copying SDP was ' + msg)
  } catch (err) {
    log('Unable to copy SDP ' + err)
  }
}

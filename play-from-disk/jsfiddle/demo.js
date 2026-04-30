// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

/* eslint-env browser */

let pc = new RTCPeerConnection({
  iceServers: [
    {
      urls: 'stun:stun.l.google.com:19302'
      // urls: 'stun:192.168.88.15:3478'      
    }
  ]
})
let log = msg => {
  document.getElementById('div').innerHTML += msg + '<br>'
}

// WebSocket连接实例
let ws = null;

// ========== 初始化WebSocket连接 ==========
function initWebSocket() {
  ws = new WebSocket('ws://localhost:10010/ws');

  ws.onopen = () => {
    log('WebSocket已连接到10010端口');
  };

  // 接收后端消息：解析SignalingMessage格式并处理
  ws.onmessage = (event) => {
    log('收到后端返回的信令消息1:',event);
    log('收到后端返回的信令消息:',event.data);
    try {
      console.log("event.data:")
      console.log(event.data)
      // 解析后端返回的SignalingMessage
      const signalingMsg = JSON.parse(event.data);
      // 处理Answer类型的SDP
      if (signalingMsg.type === 'answer' && signalingMsg.sdp) {
        // 回写到remoteSessionDescription控件
        document.getElementById('remoteSessionDescription').value = signalingMsg.sdp;
        // 自动连接
        window.startSession();
      } // 处理Candidate类型（ICE候选）     
      else if (signalingMsg.type === 'candidate' && signalingMsg.candidate) {
        try {
          // 添加ICE候选到PeerConnection
          pc.addIceCandidate(new RTCIceCandidate(signalingMsg.candidate))
            .then(() => log('ICE候选添加成功'))
            .catch(err => log('ICE候选添加失败：' + err));
        } catch (e) {
          log('解析ICE候选失败：' + e);
        }
      } else if (signalingMsg.type === 'error' && signalingMsg.error) {
        log("error:")
        log(signalingMsg.error)
      } else {
        log(event.data)
        console.log("event.data:")
        console.log(event.data)
      }
    } catch (e) {
      log('处理后端信令失败：' + e.message);
    }
  };

  ws.onclose = () => {
    log('WebSocket连接已关闭，3秒后重连...');
    setTimeout(initWebSocket, 3000);
  };

  ws.onerror = (error) => {
    log('WebSocket连接错误：' + error);
  };
}

// ========== ontrack事件：分离音频/视频控件 ==========
pc.ontrack = function (event) {
  log(`收到${event.track.kind}轨道`);
  // 视频轨道
  // if (event.track.kind === 'video') {
  //   var el = document.createElement(event.track.kind)
  //   el.srcObject = event.streams[0]
  //   el.autoplay = true
  //   el.controls = true
  //   document.getElementById('remoteVideos').appendChild(el)
  // }

  // 音频轨道
  if (event.track.kind === 'audio') {
    const audioElement = document.createElement('audio');
    audioElement.srcObject = event.streams[0];
    audioElement.autoplay = true;
    audioElement.controls = true;
    document.getElementById('remoteAudios').appendChild(audioElement);
  }
}

pc.oniceconnectionstatechange = e => log(pc.iceConnectionState)

// ========== 改造onicecandidate：发送Candidate类型信令 ==========
pc.onicecandidate = event => {
  if (event.candidate) {
    // 构造Candidate类型的SignalingMessage
    const candidateMsg = {
      type: "candidate",
      candidate: event.candidate, // 直接传递ICE候选对象
      sdp: "" // 候选消息无需SDP
    };
    // 发送ICE候选到后端
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(candidateMsg));
      log('发送ICE候选到后端：' + JSON.stringify(candidateMsg));
    } else {
      log('WebSocket未连接，无法发送ICE候选');
    }
  } else {
    // ICE候选收集完成，发送Offer类型的SDP
    const localSdp = btoa(JSON.stringify(pc.localDescription));
    document.getElementById('localSessionDescription').value = localSdp;
    log('本地SDP生成完成，发送Offer到后端');
    
    // 构造Offer类型的SignalingMessage
    const offerMsg = {
      type: "offer",
      sdp: localSdp, // base64编码的SDP
      candidate: null // Offer消息无需Candidate
    };

    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(offerMsg));
    } else {
      log('WebSocket未连接，无法发送Offer SDP');
    }
  }
}

// 配置音视频收发
// pc.addTransceiver('video', {'direction': 'sendrecv'})
pc.addTransceiver('audio', {'direction': 'sendrecv'})

// 初始化WebSocket
initWebSocket();

// 创建Offer
pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)

// 建立会话（原有逻辑，适配新的SDP格式）
window.startSession = () => {
  let sd = document.getElementById('remoteSessionDescription').value
  if (sd === '') {
    return alert('Session Description must not be empty')
  }

  try {
    pc.setRemoteDescription(JSON.parse(atob(sd)))
    log('已设置远端Answer SDP，WebRTC连接中...');
  } catch (e) {
    alert(e)
    log('设置远端SDP失败：' + e.message);
  }
}

// 页面卸载时关闭WebSocket

// ====================== 新增：WebRTC 推送麦克风音频到后端 ======================
// 本地音频流（仅麦克风）
let localAudioStream = null;

/**
 * 初始化本地麦克风：获取权限 + 预览显示 + 加入 WebRTC 连接推流
 */
async function initLocalMicrophone() {
  try {
    // 仅获取麦克风权限
    localAudioStream = await navigator.mediaDevices.getUserMedia({
      audio: true,
      video: false
    });
    log('✅ 麦克风已成功获取');

    // 页面显示本地麦克风预览
    const audioEl = document.createElement('audio');
    audioEl.srcObject = localAudioStream;
    audioEl.autoplay = true;
    audioEl.controls = true;
    audioEl.muted = true; // 避免本地回音
    document.getElementById('localMicrophone').appendChild(audioEl);

    // 核心：把麦克风轨道加入 WebRTC PeerConnection，自动推送给后端
    localAudioStream.getAudioTracks().forEach(track => {
      if (typeof pc !== 'undefined' && pc) {
        pc.addTrack(track, localAudioStream);
        log('✅ 麦克风音频轨道已加入 WebRTC 推流');
      }
    });

  } catch (e) {
    log('❌ 麦克风权限失败：' + e.message);
  }
}

/**
 * 监听 WebRTC 连接创建成功后，自动启动麦克风
 */
function waitForPeerConnection() {
  const check = setInterval(() => {
    if (typeof pc !== 'undefined' && pc) {
      clearInterval(check);
      initLocalMicrophone(); // 初始化麦克风
    }
  }, 300);
}

// 页面加载后等待 WebRTC 连接就绪
window.addEventListener('load', () => {
  waitForPeerConnection();
});

// 页面关闭时释放麦克风资源
window.addEventListener('beforeunload', () => {
  if (localAudioStream) {
    localAudioStream.getTracks().forEach(track => track.stop());
    log('✅ 麦克风资源已释放');
  }
});


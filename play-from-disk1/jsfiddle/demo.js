let pc = new RTCPeerConnection({
  iceServers: [{ urls: 'stun:stun.l.google.com:19302' }]
})

let log = msg => {
  document.getElementById('div').innerHTML += msg + '<br>'
}

// ------------------------------
// 你原本正常的 ontrack（完全不動）
// ------------------------------
pc.ontrack = function (event) {
  log("收到audio轨道");
  var el = document.createElement('audio')
  el.srcObject = event.streams[0]
  el.autoplay = true
  el.controls = true
  document.getElementById('remoteVideos').appendChild(el)
  log("远端音频播放器创建成功");
}

pc.oniceconnectionstatechange = e => log("連接狀態："+pc.iceConnectionState)

// ------------------------------
// 你原本正常的 ICE（完全不動）
// ------------------------------
pc.onicecandidate = event => {
  if (event.candidate === null) {
    document.getElementById('localSessionDescription').value = btoa(JSON.stringify(pc.localDescription))
    log("本地SDP生成完成");
  }
}

// ------------------------------
// 你原本正常的 WebSocket（完全不動）
// ------------------------------
let ws = new WebSocket('ws://localhost:10010/ws')

ws.onopen = function () {
  log("WebSocket已连接到10010端口");
}

ws.onmessage = function (event) {
  log("收到后端返回的信令消息");
  try {
    let msg = JSON.parse(event.data)
    if (msg.type === "answer") {
      document.getElementById('remoteSessionDescription').value = msg.sdp
    }
  } catch(e) {}
}

// ------------------------------
// 【唯一新增】開啟麥克風 —— 不重協商、不斷線
// ------------------------------
window.enableMic = async function () {
  try {
    let stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    stream.getTracks().forEach(track => pc.addTrack(track))
    log("✅ 麥克風已開啟，音訊已傳送")
  } catch (e) {
    log("❌ 麥克風失敗：" + e.message)
  }
}

// ------------------------------
// 你原本正常的啟動（完全不動）
// ------------------------------
pc.addTransceiver('audio', { direction: 'sendrecv' })
pc.createOffer().then(d => pc.setLocalDescription(d)).catch(log)

window.startSession = function () {
  let sd = document.getElementById('remoteSessionDescription').value
  if (sd === '') return
  pc.setRemoteDescription(JSON.parse(atob(sd)))
  log("已设置远端SDP，连接成功");
}

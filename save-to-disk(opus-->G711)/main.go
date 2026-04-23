// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/gorilla/websocket"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"
	"github.com/pion/opus"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/ivfwriter"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
	"github.com/zaf/g711"
)

var (
	wsConn  *websocket.Conn
	wsMutex sync.Mutex

	oggFile *oggwriter.OggWriter
	ivfFile *ivfwriter.IVFWriter

	// Opus 解码器 48kHz 立体声
	// opusDecoder = opus.NewDecoder(48000, 2)
	// 48000 采样率 + 1 声道（WebRTC 浏览器默认都是单声道！）
	opusDecoder, _ = opus.NewDecoderWithOutput(48000, 2)
)

const (
	WEBSOCKET_HOST = "192.168.88.51"
	AUDIO_WS_PORT  = 8001
)

// connectWebSocket WebSocket 连接
func connectWebSocket() {
	wsURL := fmt.Sprintf("ws://%s:8001", WEBSOCKET_HOST)
	for {
		fmt.Printf("🔌 连接 WebSocket: %s\n", wsURL)
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err == nil {
			wsConn = conn
			fmt.Println("✅ WebSocket 连接成功")
			return
		}
		fmt.Printf("❌ 连接失败: %v，3秒后重试\n", err)
		time.Sleep(3 * time.Second)
	}
}

// sendG711 发送 G711 裸流
func sendG711(data []byte) error {
	if wsConn == nil {
		return fmt.Errorf("WebSocket连接未建立")
	}
	wsMutex.Lock()
	defer wsMutex.Unlock()
	return wsConn.WriteMessage(websocket.BinaryMessage, data)
}

// 重点：zaf/g711 正确转码方法（无 Writer 依赖，直接函数转换）
func pcm16ToG711ALaw(pcm []int16) []byte {
	out := make([]byte, len(pcm))
	for i := range pcm {
		// zaf/g711 库官方标准方法
		// out[i] = g711.EncodeAlaw(pcm[i])
		out[i] = g711.EncodeAlawFrame(pcm[i])
		// out[i] = g711.EncodeAlaw(pcm[i]) // 正确方法
	}
	return out
}

func saveToDisk(writer media.Writer, track *webrtc.TrackRemote) {
	defer func() { _ = writer.Close() }()

	// 预获取Codec信息，避免重复调用
	codec := track.Codec()
	glog.Errorf("codec.MimeType:%s", codec.MimeType)
	isOpus := strings.EqualFold(codec.MimeType, webrtc.MimeTypeOpus)
	// Opus每帧样本数（48kHz采样率，20ms帧长=960样本/帧）
	// opusFrameSamples := 960
	// pcmBytes := make([]byte, 960*2)
	pcmBuf := make([]int16, 960)
	// 根据track声道数调整（默认2声道，立体声）
	var channelCount uint16 = 2
	if codec.Channels != 0 {
		channelCount = codec.Channels
	}

	glog.Errorf("channelCount:%d", channelCount)

	for {
		rtpPacket, attributes, err := track.ReadRTP()
		if err != nil {
			fmt.Println("读取 RTP 结束:", err)
			return
		}
		glog.Error(attributes)

		// 写入文件
		if err := writer.WriteRTP(rtpPacket); err != nil {
			glog.Error(err)
			return
		}

		// 实时转码：Opus -> PCM16 -> G711 A-law
		if isOpus {
			// // 解码
			// monoPCM, err := handleOpusRTP(rtpPacket.Payload)
			// if err != nil {
			// 	glog.Error("Opus解码失败:", err)
			// 	continue
			// }

			// // 转G711发给设备
			// g711Data := pcm16ToG711ALaw(monoPCM)
			// sendG711(g711Data)

			// opusData := rtpPacket.Payload

			// --- 【核心修复】开始：解析并剥离 RED 头部 ---

			payload := rtpPacket.Payload
			if len(payload) == 0 {
				continue
			}

			opusData := payload
			// 解码Opus到PCM16
			n, err := opusDecoder.DecodeToInt16(opusData, pcmBuf)

			if err != nil {
				glog.Errorf("Opus解码失败:%s Payload[0]:%X payload长度:%d", err.Error(), rtpPacket.Payload[0], len(opusData))
				continue
			}
			if n <= 0 {
				glog.Error("解码出0个样本，跳过")
				continue
			}

			glog.Error("before pcm16ToG711ALaw data:%x", pcmBuf[:n])
			// 转码为G711 A-law
			g711Data := pcm16ToG711ALaw(pcmBuf[:n])
			// 修复3：WebSocket发送增加错误处理
			if err := sendG711(g711Data); err != nil {
				glog.Error("WebSocket发送G711失败:", err)
				// 可选：触发WebSocket重连
				go connectWebSocket()
			} else {
				glog.V(2).Infof("发送G711数据：%d字节（PCM样本数：%d）", len(g711Data), n)
			}
		}
	}
}

func main() {
	// 启动 WebSocket
	if err := openAudioServer(WEBSOCKET_HOST); err != nil {
		glog.Error(err)
	}
	go connectWebSocket()

	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000},
		PayloadType:        96,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		glog.Error(err)
		return
	}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000},
		PayloadType:        111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		glog.Error(err)
		return
	}

	// Create a InterceptorRegistry. This is the user configurable RTP/RTCP Pipeline.
	// This provides NACKs, RTCP Reports and other features. If you use `webrtc.NewPeerConnection`
	// this is enabled by default. If you are manually managing You MUST create a InterceptorRegistry
	// for each PeerConnection.
	interceptorRegistry := &interceptor.Registry{}

	// Register a intervalpli factory
	// This interceptor sends a PLI every 3 seconds. A PLI causes a video keyframe to be generated by the sender.
	// This makes our video seekable and more error resilent, but at a cost of lower picture quality and higher bitrates
	// A real world application should process incoming RTCP packets from viewers and forward them to senders
	intervalPliFactory, err := intervalpli.NewReceiverInterceptor()
	if err != nil {
		panic(err)
	}
	interceptorRegistry.Add(intervalPliFactory)
	// Use the default set of Interceptors
	if err = webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		panic(err)
	}

	// Create the API object with the MediaEngine
	api := webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(interceptorRegistry))
	// Prepare the configuration
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	}

	// Create a new RTCPeerConnection
	peerConnection, err := api.NewPeerConnection(config)
	if err != nil {
		panic(err)
	}
	// Allow us to receive 1 audio track, and 1 video track
	if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio); err != nil {
		panic(err)
	} else if _, err = peerConnection.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo); err != nil {
		panic(err)
	}

	oggFile, err := oggwriter.New("output.ogg", 48000, 2)
	if err != nil {
		panic(err)
	}
	ivfFile, err := ivfwriter.New("output.ivf", ivfwriter.WithCodec("video/VP8"))
	if err != nil {
		panic(err)
	}

	// Set a handler for when a new remote track starts, this handler saves buffers to disk as
	// an ivf file, since we could have multiple video tracks we provide a counter.
	// In your application this is where you would handle/process video
	peerConnection.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) { //nolint: revive
		codec := track.Codec()
		if strings.EqualFold(codec.MimeType, webrtc.MimeTypeOpus) {
			fmt.Println("Got Opus track, saving to disk as output.opus (48 kHz, 2 channels)")
			saveToDisk(oggFile, track)
		} else if strings.EqualFold(codec.MimeType, webrtc.MimeTypeVP8) {
			fmt.Println("Got VP8 track, saving to disk as output.ivf")
			saveToDisk(ivfFile, track)
		}
	})

	// Set the handler for ICE connection state
	// This will notify you when the peer has connected/disconnected
	peerConnection.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("Connection State has changed %s \n", connectionState.String())

		if connectionState == webrtc.ICEConnectionStateConnected {
			fmt.Println("Ctrl+C the remote client to stop the demo")
		} else if connectionState == webrtc.ICEConnectionStateFailed || connectionState == webrtc.ICEConnectionStateClosed {
			if closeErr := oggFile.Close(); closeErr != nil {
				panic(closeErr)
			}

			if closeErr := ivfFile.Close(); closeErr != nil {
				panic(closeErr)
			}

			fmt.Println("Done writing media files")

			// Gracefully shutdown the peer connection
			if closeErr := peerConnection.Close(); closeErr != nil {
				panic(closeErr)
			}

			os.Exit(0)
		}
	})

	// Wait for the offer to be pasted
	offer := webrtc.SessionDescription{}
	decode(readUntilNewline(), &offer)

	// Set the remote SessionDescription
	err = peerConnection.SetRemoteDescription(offer)
	if err != nil {
		panic(err)
	}

	// Create answer
	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	} // Create channel that is blocked until ICE Gathering is complete
	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)

	// Sets the LocalDescription, and starts our UDP listeners
	err = peerConnection.SetLocalDescription(answer)
	if err != nil {
		panic(err)
	}

	// Block until ICE Gathering is complete, disabling trickle ICE
	// we do this because we only can exchange one signaling message
	// in a production application you should exchange ICE Candidates via OnICECandidate
	<-gatherComplete

	// Output the answer in base64 so we can paste it in browser
	fmt.Println(encode(peerConnection.LocalDescription()))

	// Block forever
	select {}
}

func readUntilNewline() (in string) {
	r := bufio.NewReader(os.Stdin)
	for {
		in, _ = r.ReadString('\n')
		if in = strings.TrimSpace(in); len(in) > 0 {
			break
		}
	}
	return
}

func encode(obj *webrtc.SessionDescription) string {
	b, _ := json.Marshal(obj)
	return base64.StdEncoding.EncodeToString(b)
}

func decode(in string, obj *webrtc.SessionDescription) {
	b, _ := base64.StdEncoding.DecodeString(in)
	_ = json.Unmarshal(b, obj)
}

//****************begin*************************************

type VisualIntercomRequest struct {
	Cmd  string `json:"cmd"`
	Open bool   `json:"open"`
}

type Response struct {
	Code int `json:"code"`
}

func openAudioServer(ip string) error {
	url := fmt.Sprintf("http://%s:8000", ip)
	client := &http.Client{Timeout: 10 * time.Second}
	var wg sync.WaitGroup
	var res1 Response
	var err1 error

	wg.Add(1)
	go func() {
		defer wg.Done()
		reqBody := VisualIntercomRequest{Cmd: "set visual intercom", Open: true}
		res1, err1 = postRequest(client, url, reqBody)
	}()

	wg.Wait()
	if err1 != nil {
		fmt.Println("设备音频请求失败:", err1)
		return err1
	}
	if res1.Code == 0 {
		fmt.Println("设备音频已开启")
	} else {
		fmt.Println("设备音频未开启")
	}
	return nil
}

func closeAudioServer(ip string) error {
	url := fmt.Sprintf("http://%s:8000", ip)
	client := &http.Client{Timeout: 10 * time.Second}
	reqBody := VisualIntercomRequest{Cmd: "set visual intercom", Open: false}
	res1, err1 := postRequest(client, url, reqBody)

	if err1 != nil {
		fmt.Println("设备音频请求失败:", err1)
		return err1
	}
	if res1.Code == 0 {
		fmt.Println("设备音频已关闭")
	} else {
		fmt.Println("设备音频未关闭")
	}
	return nil
}

func postRequest(client *http.Client, url string, reqBody any) (Response, error) {
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	var response Response
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return Response{}, err
	}
	return response, nil
}

//&**************end****************************************

func handleOpusRTP(rtpPayload []byte) ([]int16, error) {
	// 48000Hz 20ms 双声道 = 3840 字节 PCM
	pcmBytes := make([]byte, 3840)

	// ✅ 正确：双声道必须用 Decode()
	_, isStereo, err := opusDecoder.Decode(rtpPayload, pcmBytes)
	if err != nil {
		return nil, err
	}

	// --------------------------
	// 修复：[]byte → []int16（标准写法，无编译错）
	// --------------------------
	sampleCount := len(pcmBytes) / 2
	pcm := make([]int16, sampleCount)
	for i := 0; i < sampleCount; i++ {
		pcm[i] = int16(binary.LittleEndian.Uint16(pcmBytes[i*2:]))
	}

	// --------------------------
	// 立体声 → 单声道（设备必须要单声道）
	// --------------------------
	if isStereo && len(pcm) >= 2 {
		mono := make([]int16, len(pcm)/2)
		for i := 0; i < len(mono); i++ {
			left := pcm[i*2]
			right := pcm[i*2+1]
			mono[i] = (left + right) / 2
		}
		return mono, nil
	}

	return pcm, nil
}

// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/gorilla/websocket"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/intervalpli"

	// "github.com/pion/opus"
	"github.com/hraban/opus"
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
	opusDecoder, _ = opus.NewDecoder(48000, 2)
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
	fmt.Println("sendG711:", data)
	return wsConn.WriteMessage(websocket.BinaryMessage, data)
}

// 重点：zaf/g711 正确转码方法（无 Writer 依赖，直接函数转换）
func pcm16ToG711ALaw(pcm []int16) []byte {
	out := make([]byte, len(pcm))
	for i := range pcm {
		// zaf/g711 库官方标准方法
		// out[i] = g711.EncodeAlaw(pcm[i]) // 正确方法
		out[i] = g711.EncodeAlawFrame(pcm[i])
	}
	return out
}

func saveToDisk1(writer media.Writer, track *webrtc.TrackRemote) {
	defer func() { _ = writer.Close() }()

	// 预获取Codec信息，避免重复调用
	codec := track.Codec()
	glog.Errorf("codec.MimeType:%s", codec.MimeType)
	isOpus := strings.EqualFold(codec.MimeType, webrtc.MimeTypeOpus)
	// Opus每帧样本数（48kHz采样率，20ms帧长=960样本/帧）
	// opusFrameSamples := 960
	// pcmBytes := make([]byte, 960*2)
	// pcmBuf := make([]int16, 960)

	// 根据track声道数调整（默认2声道，立体声）
	var channelCount uint16 = 2
	if codec.Channels != 0 {
		channelCount = codec.Channels
	}

	// 最大帧长 120ms @ 48kHz = 5760 样本/声道
	// 立体声（2声道）则需要 11520 个 int16
	const maxFrameSizeSamplesPerChannel = 5760 // 120ms @ 48kHz
	pcmBuf := make([]int16, maxFrameSizeSamplesPerChannel*int(channelCount))

	if isOpus {
		// 关键修复 1: 必须根据 SDP 协商的参数初始化解码器
		// WebRTC 默认通常是 48000Hz，但也可能是其他值
		sampleRate := int(codec.ClockRate)
		channelCount := int(codec.Channels)

		glog.Errorf("sampleRate:%d,channelCount:%d", sampleRate, channelCount)
		if sampleRate == 0 {
			sampleRate = 48000
		} // 兜底
		if channelCount == 0 {
			channelCount = 2
		} // 兜底
		// var err error
		// // 初始化 Opus 解码器
		// err = opusDecoder.Init(sampleRate, channelCount)
		// if err != nil {
		// 	glog.Errorf("创建 Opus 解码器失败: %v", err)
		// 	return
		// }
	}

	// // 关键修复 2: 增大缓冲区以容纳不同帧长的数据
	// // Opus 最大帧长通常为 120ms。48kHz * 0.12s = 5760 样本。
	// // 为了安全，我们分配足够大的缓冲区
	// maxFrameSizeMs := 120
	// // 估算最大样本数：(ClockRate / 1000) * maxFrameSizeMs
	// // maxSamples := int(track.Codec().ClockRate / 1000 * maxFrameSizeMs)
	// maxSamples := int(track.Codec().ClockRate * maxFrameSizeMs / 1000)
	// if maxSamples < 960 {
	// 	maxSamples = 960
	// }

	// pcmBuf := make([]int16, maxSamples)

	glog.Errorf("channelCount:%d", channelCount)
	for {
		// rtpPacket, attributes, err := track.ReadRTP()
		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			fmt.Println("读取 RTP 结束:", err)
			return
		}
		// glog.Error(attributes)

		// 写入文件
		if err := writer.WriteRTP(rtpPacket); err != nil {
			glog.Error(err)
			return
		}

		// 实时转码：Opus -> PCM16 -> G711 A-law
		if isOpus {
			opusFrame := rtpPacket.Payload
			if len(opusFrame) == 0 {
				continue
			}
			// 解码Opus到PCM16
			n, err := opusDecoder.Decode(opusFrame, pcmBuf)
			if err != nil {
				glog.Errorf("Opus解码失败:%s Payload[0]:%x payload长度:%d，payload:%x ", err.Error(), rtpPacket.Payload[0], len(opusFrame), opusFrame)
				continue
			}
			if n <= 0 {
				glog.Error("解码出0个样本，跳过")
				continue
			}

			// glog.Error("before pcm16ToG711ALaw data:%x", pcmBuf[:n])
			// 转码为G711 A-law

			// g711Data := pcm16ToG711ALaw(pcmBuf[:n])
			g711Data, err := convertToG711WithFFmpeg(pcmBuf[:n])
			if err != nil {
				glog.Error(err)
				continue
			}
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

func saveToDisk(writer media.Writer, track *webrtc.TrackRemote) {
	defer func() { _ = writer.Close() }()

	codec := track.Codec()
	isOpus := strings.EqualFold(codec.MimeType, webrtc.MimeTypeOpus)

	var channelCount uint16 = 2
	if codec.Channels != 0 {
		channelCount = codec.Channels
	}

	decoder, err := opus.NewDecoder(48000, int(channelCount))
	if err != nil {
		glog.Errorf("创建 Opus 解码器失败: %v", err)
		return
	}

	// 固定 20ms 帧缓冲区
	pcmBuf := make([]int16, 960*int(channelCount))

	for {
		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			glog.Error("读取 RTP 结束:", err)
			return
		}

		// 写入文件
		if err := writer.WriteRTP(rtpPacket); err != nil {
			glog.Error(err)
			return
		}

		if isOpus && len(rtpPacket.Payload) > 0 {
			n, err := decoder.Decode(rtpPacket.Payload, pcmBuf)
			if err != nil {
				glog.Errorf("Opus解码失败: %v", err)
				continue
			}

			// 取实际 PCM 数据
			pcmData := pcmBuf[:n*int(channelCount)]

			// 转换并发送...
			g711Data, err := convertToG711WithFFmpeg(pcmData)
			if err != nil {
				glog.Error(err)
				continue
			}
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
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:192.168.88.15:19302"}}},
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
	// 启动 WebSocket
	go connectWebSocket()
	if err := openAudioServer(WEBSOCKET_HOST); err != nil {
		glog.Error(err)
	}
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

func convertToG711WithFFmpeg(pcm48kHzStereo []int16) ([]byte, error) {
	cmd := exec.Command("ffmpeg",
		"-f", "s16le",
		"-ar", "48000",
		"-ac", "2",
		"-i", "pipe:0",
		"-ar", "8000",
		"-ac", "1",
		"-c:a", "pcm_alaw",
		"-f", "alaw",
		"pipe:1",
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// 转换 int16 到 []byte
	pcmBytes := make([]byte, len(pcm48kHzStereo)*2)
	for i, sample := range pcm48kHzStereo {
		pcmBytes[i*2] = byte(sample & 0xFF)
		pcmBytes[i*2+1] = byte(sample >> 8)
	}

	// 写入数据
	go func() {
		defer stdin.Close()
		stdin.Write(pcmBytes)
	}()

	// 读取输出
	g711Data, err := io.ReadAll(stdout)
	if err != nil {
		return nil, err
	}

	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	return g711Data, nil
}

func convertToG711WithFFmpeg1(pcm48kHzStereo []int16) ([]byte, error) {
	cmd := exec.Command("ffmpeg",
		"-f", "s16le", // 输入格式：16位 PCM
		"-ar", "48000", // 输入采样率
		"-ac", "2", // 输入声道数（立体声）
		"-i", "pipe:0", // 从 stdin 读取
		"-ar", "8000", // 输出采样率
		"-ac", "1", // 输出声道数（单声道）
		"-c:a", "pcm_alaw", // G711 A-law 编码
		"-f", "alaw", // 输出格式
		"pipe:1", // 输出到 stdout
	)

	// 获取 stdin 管道
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	// 获取 stdout 管道
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	// 启动命令
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// 将 PCM 数据写入 stdin（在 goroutine 中，避免阻塞）
	go func() {
		defer stdin.Close()

		// 将 int16 转换为 []byte（小端序）
		pcmBytes := make([]byte, len(pcm48kHzStereo)*2)
		for i, sample := range pcm48kHzStereo {
			pcmBytes[i*2] = byte(sample & 0xFF)
			pcmBytes[i*2+1] = byte(sample >> 8)
		}

		stdin.Write(pcmBytes)
	}()

	// 读取输出
	g711Data, err := io.ReadAll(stdout)
	if err != nil {
		return nil, err
	}

	// 等待命令结束
	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	return g711Data, nil
}

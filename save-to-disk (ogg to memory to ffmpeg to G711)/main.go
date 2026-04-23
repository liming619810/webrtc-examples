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
	"path/filepath"
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
	ICEServerHost = "stun:stun.l.google.com:19302"
	// ICEServer = "stun:192.168.88.15:3478"
	wsConn  *websocket.Conn
	wsMutex sync.Mutex

	oggFile     *oggwriter.OggWriter
	ivfFile     *ivfwriter.IVFWriter
	oggFileName = "output.ogg"          // 你的 ogg 文件
	pushPeriod  = 20 * time.Millisecond // 实时语音分片发送（20ms 标准）
	frameSize   = 160                   // 8000Hz + 20ms = 160 字节/G711

	// 全局等待组，让 main 等待推送完成
	// globalWg sync.WaitGroup
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
	glog.Error("sendG711:", data)
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

// 获取内存文件系统路径（Linux 使用 /dev/shm，其他系统回退到系统临时目录）
func getMemoryFilePath(baseName string) string {
	// if runtime.GOOS == "linux" {
	// 	// /dev/shm 是 tmpfs，完全在内存中
	return filepath.Join("/dev/shm", baseName)
	// }
	// // 其他系统仍使用临时目录（尽量靠近内存，但无法保证纯内存）
	// return filepath.Join(os.TempDir(), baseName)
}

func saveToDiskChunked2(initialWriter media.Writer, track *webrtc.TrackRemote) {
	chunkSize := 1 * time.Second
	chunkIdx := 0
	ticker := time.NewTicker(chunkSize)
	defer ticker.Stop()

	// 当前活跃的 writer 和对应的文件路径
	var currentWriter media.Writer = initialWriter
	var currentFilePath string

	// 初始化第一个文件（内存路径）
	baseName := fmt.Sprintf("output_%d.ogg", chunkIdx)
	currentFilePath = getMemoryFilePath(baseName)
	var err error
	currentWriter, err = oggwriter.New(currentFilePath, 48000, 2)
	if err != nil {
		panic(err)
	}

	// 分片处理循环
	for {
		select {
		case <-ticker.C:
			// 关闭当前分片文件
			if err := currentWriter.Close(); err != nil {
				glog.Error("关闭 Ogg 写入器失败:", err)
			}

			// 启动转码推送 goroutine（处理已完成的文件）
			go func(filePath string, idx int) {
				// 转码并推送
				g711Stream, err := OggToG711Stream(filePath)
				if err != nil {
					glog.Error("打开 G711 流失败:", err)
					return
				}
				defer g711Stream.Close()
				PushG711ToWebSocket(wsConn, g711Stream)
				// 推送完成后删除内存文件（释放空间）
				_ = os.Remove(filePath)
				fmt.Printf("分片 %d 处理完成，已删除内存文件\n", idx)
			}(currentFilePath, chunkIdx)

			// 准备下一个分片
			chunkIdx++
			nextBaseName := fmt.Sprintf("output_%d.ogg", chunkIdx)
			currentFilePath = getMemoryFilePath(nextBaseName)
			currentWriter, err = oggwriter.New(currentFilePath, 48000, 2)
			if err != nil {
				glog.Error("创建新 Ogg 写入器失败:", err)
				break
			}

		default:
			// 读取 RTP 并写入当前 writer
			rtpPacket, _, err := track.ReadRTP()
			if err != nil {
				fmt.Println("读取 RTP 结束:", err)
				return
			}
			if err := currentWriter.WriteRTP(rtpPacket); err != nil {
				glog.Error(err)
				return
			}
		}
	}

	// 循环结束后关闭最后一个 writer（如有剩余数据）
	if currentWriter != nil {
		_ = currentWriter.Close()
		// 处理最后一个分片
		go func(filePath string, idx int) {
			g711Stream, err := OggToG711Stream(filePath)
			if err == nil {
				defer g711Stream.Close()
				PushG711ToWebSocket(wsConn, g711Stream)
			}
			_ = os.Remove(filePath)
		}(currentFilePath, chunkIdx)
	}
}

func saveToDiskChunked(writer media.Writer, track *webrtc.TrackRemote) {
	chunkSize := 1 * time.Second // 1s 分片
	chunkIdx := 0
	ticker := time.NewTicker(chunkSize)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 每 1s 关闭当前 ogg 文件，转码推送
			writer.Close()
			chunkFileName := fmt.Sprintf("output_%d.ogg", chunkIdx)
			os.Rename("output.ogg", chunkFileName)

			// 实时转码当前分片
			g711Stream, _ := OggToG711Stream(chunkFileName)
			go PushG711ToWebSocket(wsConn, g711Stream)

			// 新建 ogg 文件，继续写入
			writer, _ = oggwriter.New("output.ogg", 48000, 2)
			chunkIdx++
		default:
			// 读取 RTP 并写入
			rtpPacket, _, err := track.ReadRTP()
			if err != nil {
				break
			}
			writer.WriteRTP(rtpPacket)
		}
	}
}

func saveToDisk3(writer media.Writer, track *webrtc.TrackRemote) {
	defer func() { _ = writer.Close() }()

	for {
		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			fmt.Println("读取 RTP 结束:", err)
			break
		}

		// 只做一件事：写入 ogg 文件（完全不改动）
		if err := writer.WriteRTP(rtpPacket); err != nil {
			glog.Error(err)
			break
		}
	}

	fmt.Println("✅ output.ogg 已保存到磁盘，开始转码并推送 WebSocket...")

	// ==============================
	// 【新增逻辑】
	// 文件保存完成后，读取 output.ogg → ffmpeg 转码 → WebSocket
	// ==============================
	var transcodeWg sync.WaitGroup
	transcodeWg.Add(1)
	go func() {
		defer transcodeWg.Done()

		// 等待 WebSocket 连接
		connWait := time.NewTimer(10 * time.Second)
		defer connWait.Stop()
		for {
			if wsConn != nil {
				break
			}
			select {
			case <-connWait.C:
				glog.Error("WebSocket 连接超时，退出")
				return
			case <-time.After(100 * time.Millisecond):
			}
		}

		// ==============================================
		// ffmpeg：直接读取本地文件 output.ogg → 转 G711 → 输出到管道
		// ==============================================
		ffmpegCmd := exec.Command(
			"ffmpeg",
			"-i", "output.ogg", // 直接读取已保存的 ogg 文件
			"-ar", "8000", // 8000 采样率
			"-ac", "1", // 单声道
			"-c:a", "pcm_alaw", // G711 alaw
			"-f", "alaw",
			"-loglevel", "error",
			"pipe:1", // 输出 G711 到标准输出
		)

		// 获取 ffmpeg 输出管道（G711 数据）
		stdout, err := ffmpegCmd.StdoutPipe()
		if err != nil {
			glog.Error("ffmpeg stdout 错误:", err)
			return
		}

		// 启动 ffmpeg
		if err := ffmpegCmd.Start(); err != nil {
			glog.Error("启动 ffmpeg 失败:", err)
			return
		}
		defer func() { _ = ffmpegCmd.Wait() }()

		// 读取 ffmpeg 输出的 G711 并推送 WebSocket
		buf := make([]byte, 160) // 20ms G711 标准帧大小
		for {
			n, err := stdout.Read(buf)
			if err != nil {
				if err == io.EOF {
					fmt.Println("✅ ffmpeg 转码完成，文件推送完毕")
				} else {
					glog.Error("读取 G711 失败:", err)
				}
				break
			}

			// 推送到 WebSocket
			if err := sendG711(buf[:n]); err != nil {
				glog.Error("发送 WebSocket 失败:", err)
				break
			}

			// 20ms 一帧，符合语音流节奏
			time.Sleep(20 * time.Millisecond)
		}
	}()

	// 等待推送完成
	transcodeWg.Wait()
	fmt.Println("✅ 全部流程完成：保存 ogg → 转码 → WebSocket 推送")
}

func saveToDisk2(writer media.Writer, track *webrtc.TrackRemote) {
	defer func() { _ = writer.Close() }()

	// 预获取Codec信息，避免重复调用
	// codec := track.Codec()
	// var channelCount uint16 = 2
	// if codec.Channels != 0 {
	// 	channelCount = codec.Channels
	// }
	// glog.Errorf("channelCount:%d", channelCount)

	// ==== 新增：启动ffmpeg转码+WebSocket推送的goroutine ====
	var ffmpegCmd *exec.Cmd
	var ffmpegStdin io.WriteCloser
	var transcodeWg sync.WaitGroup

	// 初始化ffmpeg转码进程（实时从stdin读取ogg，转码为G711 alaw）
	ffmpegCmd = exec.Command(
		"ffmpeg",
		"-i", "pipe:0", // 从标准输入读取ogg数据
		"-ar", "8000", // 采样率8000Hz
		"-ac", "1", // 单声道
		"-c:a", "pcm_alaw", // 编码格式G711 alaw
		"-f", "alaw", // 输出裸流格式
		"-loglevel", "error", // 屏蔽ffmpeg日志
		"pipe:1", // 输出到标准输出
	)

	// 获取ffmpeg的标准输入（用于写入ogg数据）和标准输出（读取G711数据）
	ffmpegStdin, err := ffmpegCmd.StdinPipe()
	if err != nil {
		glog.Error("创建ffmpeg标准输入失败:", err)
	}
	ffmpegStdout, err := ffmpegCmd.StdoutPipe()
	if err != nil {
		glog.Error("创建ffmpeg标准输出失败:", err)
	}

	// 启动ffmpeg进程
	if err := ffmpegCmd.Start(); err != nil {
		glog.Error("启动ffmpeg转码失败:", err)
	}

	// 启动goroutine：从ffmpeg读取G711数据并推送到WebSocket
	transcodeWg.Add(1)
	go func() {
		defer transcodeWg.Done()
		// 等待WebSocket连接建立（最多等待10秒）
		connWait := time.NewTimer(10 * time.Second)
		defer connWait.Stop()
		for {
			if wsConn != nil {
				break
			}
			select {
			case <-connWait.C:
				glog.Error("WebSocket连接超时，停止G711推送")
				return
			case <-time.After(100 * time.Millisecond):
				continue
			}
		}

		// 实时推送G711数据到WebSocket
		buf := make([]byte, frameSize)
		for {
			n, err := io.ReadFull(ffmpegStdout, buf)
			if err != nil {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					break
				}
				glog.Error("读取G711数据失败:", err)
				break
			}

			// 发送G711数据到WebSocket
			if err := sendG711(buf[:n]); err != nil {
				glog.Error("推送G711到WebSocket失败:", err)
				break
			}

			// 保持20ms/帧的推送速率
			time.Sleep(pushPeriod)
		}
	}()

	// ==== 原有逻辑：读取RTP并写入ogg文件 + 同步写入ffmpeg标准输入 ====
	for {
		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			fmt.Println("读取 RTP 结束:", err)
			break
		}

		// 1. 写入ogg文件（原有逻辑）
		if err := writer.WriteRTP(rtpPacket); err != nil {
			glog.Error(err)
			break
		}

		// 2. 同步将RTP数据写入ffmpeg进行实时转码（关键：不落地文件直接转码）
		if ffmpegStdin != nil {
			// 将RTP包的Payload写入ffmpeg（RTP头不需要，只需要音频数据）
			glog.Error("ffmpegStdout data:", rtpPacket.Payload)
			if _, err := ffmpegStdin.Write(rtpPacket.Payload); err != nil {
				if !strings.Contains(err.Error(), "io: read/write on closed pipe") {
					glog.Error("写入ffmpeg失败:", err)
				}
				break
			}
		}
	}

	// ==== 清理资源 ====
	// 关闭ffmpeg输入，触发转码结束
	if ffmpegStdin != nil {
		_ = ffmpegStdin.Close()
	}
	// 等待ffmpeg进程退出
	_ = ffmpegCmd.Wait()
	// 等待G711推送goroutine结束
	transcodeWg.Wait()

	fmt.Println("✅ 音频保存+转码+推送完成")
	return
}

func saveToDisk1(writer media.Writer, track *webrtc.TrackRemote) {
	defer func() { _ = writer.Close() }()

	// 预获取Codec信息，避免重复调用
	codec := track.Codec()
	// isOpus := strings.EqualFold(codec.MimeType, webrtc.MimeTypeOpus)
	// Opus每帧样本数（48kHz采样率，20ms帧长=960样本/帧）
	// opusFrameSamples := 960
	// 根据track声道数调整（默认2声道，立体声）
	var channelCount uint16 = 2
	if codec.Channels != 0 {
		channelCount = codec.Channels
	}
	glog.Errorf("channelCount:%d", channelCount)

	for {
		rtpPacket, _, err := track.ReadRTP()
		if err != nil {
			fmt.Println("读取 RTP 结束:", err)
			break
		}
		// glog.Errorf("%v", attributes)

		// 写入文件
		if err := writer.WriteRTP(rtpPacket); err != nil {
			glog.Error(err)
			break
		}
	}

	// 2. 打开 G711 实时流
	g711Stream, err := OggToG711Stream(oggFileName)
	if err != nil {
		panic("打开流失败: " + err.Error())
	}
	defer g711Stream.Close()

	// 3. 开始实时推送
	fmt.Println("🚀 开始推送 G711 音频流...")
	err = PushG711ToWebSocket1(wsConn, g711Stream)
	if err != nil {
		panic("推送失败: " + err.Error())
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
		// ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
		ICEServers: []webrtc.ICEServer{{URLs: []string{ICEServerHost}}},
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
			// saveToDiskChunked(oggFile, track)
			saveToDiskChunked2(oggFile, track)
		}
		//  else if strings.EqualFold(codec.MimeType, webrtc.MimeTypeVP8) {
		// 	fmt.Println("Got VP8 track, saving to disk as output.ivf")
		// 	saveToDisk(ivfFile, track)
		// }
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

			time.Sleep(time.Second * 30)
			fmt.Println("os.Exit(0)")

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
	if err := openAudioServer(WEBSOCKET_HOST); err != nil {
		glog.Error(err)
	}
	go connectWebSocket()

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

// OggToG711Stream 实时转换 ogg 为 G711 裸流（不占内存，流式输出）
func OggToG711Stream(oggFilePath string) (io.ReadCloser, error) {
	cmd := exec.Command(
		"ffmpeg",
		"-i", oggFilePath,
		"-ar", "8000",
		"-ac", "1",
		"-c:a", "pcm_alaw",
		"-f", "alaw",
		"-",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		glog.Error(err)
		return nil, err
	}

	// 启动 ffmpeg 开始转换
	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	return stdout, nil
}

// PushG711ToWebSocket 从流中读取 G711 并实时推送到 WebSocket
func PushG711ToWebSocket1(conn *websocket.Conn, stream io.Reader) error {
	buf := make([]byte, frameSize)

	for {
		// 按语音标准 20ms 分片读取
		n, err := io.ReadFull(stream, buf)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return fmt.Errorf("读取流失败: %v", err)
		}

		// 直接发送二进制 G711 数据到 WebSocket
		if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
			return fmt.Errorf("发送失败: %v", err)
		}

		// 模拟实时播放速度（关键：否则会瞬间发完）
		time.Sleep(pushPeriod)
	}

	fmt.Println("✅ G711 音频流推送完成！")
	return nil
}

func PushG711ToWebSocket(wsConn *websocket.Conn, reader io.Reader) {
	buf := make([]byte, 160) // 8000/1/20ms 标准帧
	fmt.Println("✅ 开始推送 G711 到 WebSocket")

	for {
		n, err := reader.Read(buf)
		if err != nil {
			if err == io.EOF {
				fmt.Println("✅ G711 推送完成（EOF）")
			} else {
				fmt.Println("❌ G711 读取错误:", err)
			}
			break
		}

		glog.Error("buf[:n]:", buf[:n])
		// 🔥 必须阻塞发送，确保数据真的发出去
		err = wsConn.WriteMessage(websocket.BinaryMessage, buf[:n])
		if err != nil {
			fmt.Println("❌ WebSocket 发送失败:", err)
			break
		}
	}
}

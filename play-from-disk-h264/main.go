// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kazzmir/opus-go/opus"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/zaf/g711"
	"golang.org/x/net/websocket"
)

const (
	WEBSOCKET_HOST = "192.168.88.51"
	// WEBSOCKET_HOST       = "127.0.0.1"
	AUDIO_WS_PORT = 8001
	// AUDIO_FRAME_DURATION = 20 * time.Millisecond

	// SRC_RATE = 8000
	// DST_RATE = 48000
	// CHANNELS = 2 // 🔥 强制 2 声道 匹配你的 Offer
	// FRAME_MS = 20
)

var (
	peerConnection *webrtc.PeerConnection
	audioTrack     *webrtc.TrackLocalStaticSample
	audioTrackLock sync.Mutex
)

// 🔥 提前创建标准 2 声道音频轨道（100% 匹配浏览器 Offer）
func initAudioTrack() {
	audioTrackLock.Lock()
	defer audioTrackLock.Unlock()

	if audioTrack == nil {
		var err error
		audioTrack, err = webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{
				MimeType:  webrtc.MimeTypeOpus,
				ClockRate: 48000,
				Channels:  2, // ✅ 必须 2 声道
			},
			"audio",
			"pion",
		)
		if err != nil {
			panic(err)
		}

		_, err = peerConnection.AddTrack(audioTrack)
		if err != nil {
			panic(err)
		}
		fmt.Println("✅ 2 声道音频轨道初始化完成")
	}
}

func PlayWebsocketAudio(iceConnectedCtx context.Context) {
	openAudioServer(WEBSOCKET_HOST)
	wsUrl := fmt.Sprintf("ws://%s:%d/", WEBSOCKET_HOST, AUDIO_WS_PORT)
	ws, err := websocket.Dial(wsUrl, "", "http://localhost")
	if err != nil {
		fmt.Println("WebSocket 连接失败:", err)
		return
	}
	defer func() {
		ws.Close()
		closeAudioServer(WEBSOCKET_HOST)
	}()
	fmt.Println("✅ 音频 WebSocket 已连接")

	// ✅ 2 声道编码器
	opusEnc, err := opus.NewEncoder(48000, 2, opus.ApplicationVoIP)
	if err != nil {
		panic(err)
	}

	<-iceConnectedCtx.Done()
	fmt.Println("🚀 开始发送音频到浏览器")

	srcBuffer := make([]byte, 0, 4096)
	opusBuf := make([]byte, 4000)
	const g711FrameSize = 160 // 8000Hz 20ms = 160 字节

	for {
		buf := make([]byte, 4096)
		n, err := ws.Read(buf)
		if err != nil {
			fmt.Println("🔌 WebSocket 关闭")
			return
		}

		srcBuffer = append(srcBuffer, buf[:n]...)
		for len(srcBuffer) >= g711FrameSize {
			frame := srcBuffer[:g711FrameSize]
			srcBuffer = srcBuffer[g711FrameSize:]

			// 1. G711a → 16bit PCM
			pcm8k := g711.DecodeAlaw(frame)
			// 2. 8kHz → 48kHz
			pcm48k := resampleLinear(pcm8k, 8000, 48000)
			// 3. byte → int16
			pcmSamples := bytesToInt16(pcm48k)

			// 4. 单声道 → 双声道（浏览器必须要）
			stereo := make([]int16, len(pcmSamples)*2)
			for i := range pcmSamples {
				stereo[i*2] = pcmSamples[i]
				stereo[i*2+1] = pcmSamples[i]
			}

			// 5. Opus 编码（固定 960 采样点 = 20ms）
			ns, err := opusEnc.Encode(stereo, 960, opusBuf)
			if err != nil || ns <= 0 {
				continue
			}

			// 6. 发送给浏览器
			_ = audioTrack.WriteSample(media.Sample{
				Data:     opusBuf[:ns],
				Duration: 20 * time.Millisecond,
			})
		}
	}
}

func main() {
	var err error
	peerConnection, err = webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	})
	if err != nil {
		panic(err)
	}
	defer peerConnection.Close()

	// ✅ 关键：信令前创建轨道
	initAudioTrack()

	iceConnectedCtx, iceConnectedCtxCancel := context.WithCancel(context.Background())
	peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		fmt.Printf("Connection State: %s\n", state.String())
		if state == webrtc.ICEConnectionStateConnected {
			iceConnectedCtxCancel()
		}
	})

	offer := webrtc.SessionDescription{}
	decode(readUntilNewline(), &offer)
	// decode(BDP, &offer)
	if err = peerConnection.SetRemoteDescription(offer); err != nil {
		panic(err)
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		panic(err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(peerConnection)
	if err = peerConnection.SetLocalDescription(answer); err != nil {
		panic(err)
	}
	<-gatherComplete
	fmt.Println(encode(peerConnection.LocalDescription()))
	go PlayWebsocketAudio(iceConnectedCtx)
	select {}
}

// 工具函数
func resampleLinear(pcm []byte, srcRate, dstRate int) []byte {
	if srcRate == dstRate {
		return pcm
	}
	ratio := float64(dstRate) / float64(srcRate)
	srcSamples := len(pcm) / 2
	dstSamples := int(float64(srcSamples) * ratio)
	dst := make([]byte, dstSamples*2)

	for i := 0; i < dstSamples; i++ {
		// 计算目标采样点对应源采样点位置
		srcIdx := float64(i) / ratio
		srcIdx0 := int(srcIdx)

		// 防止越界
		if srcIdx0 >= srcSamples {
			srcIdx0 = srcSamples - 1
		}

		// 直接取最近点（简单有效，满足 WebRTC 音频）
		s0 := int16(pcm[srcIdx0*2]) | int16(pcm[srcIdx0*2+1])<<8

		// 写入目标 PCM
		dst[i*2] = byte(s0)
		dst[i*2+1] = byte(s0 >> 8)
	}

	return dst
}

func bytesToInt16(data []byte) []int16 {
	n := len(data) / 2
	r := make([]int16, n)
	for i := 0; i < n; i++ {
		r[i] = int16(data[i*2]) | int16(data[i*2+1])<<8
	}
	return r
}

func encode(obj *webrtc.SessionDescription) string {
	b, _ := json.Marshal(obj)
	return base64.StdEncoding.EncodeToString(b)
}

func decode(in string, obj *webrtc.SessionDescription) {
	b, _ := base64.StdEncoding.DecodeString(in)
	_ = json.Unmarshal(b, obj)
}

// Read from stdin until we get a newline.
func readUntilNewline() (in string) {
	var err error

	r := bufio.NewReader(os.Stdin)
	for {
		in, err = r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			panic(err)
		}

		if in = strings.TrimSpace(in); len(in) > 0 {
			break
		}
	}

	fmt.Println("")

	return
}

// 定义请求体结构体（对应 JS 中的 JSON 参数）
type VisualIntercomRequest struct {
	Cmd  string `json:"cmd"`
	Open bool   `json:"open"`
}

type FrameRateRequest struct {
	Cmd string `json:"cmd"`
	FPS int    `json:"fps"`
}

// 定义通用响应结构体（根据你的返回格式定义）
type Response struct {
	Code int `json:"code"`
	// 可以根据实际返回补充其他字段
}

func openAudioServer(ip string) error {
	// 1. 定义设备IP
	url := fmt.Sprintf("http://%s:8000", ip)

	// 2. 创建带超时的 HTTP 客户端（10秒超时，对应 axios timeout）
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// 3. 定义并发等待组
	var wg sync.WaitGroup
	// 存储两个请求的结果
	var res1 Response
	//  var res2  Response
	// 存储错误信息
	var err1 error
	// var err2  error

	// ==================== 并发发送两个POST请求 ====================
	// 第一个请求：set visual intercom
	wg.Add(1)
	go func() {
		defer wg.Done()
		reqBody := VisualIntercomRequest{
			Cmd:  "set visual intercom",
			Open: true,
		}
		res1, err1 = postRequest(client, url, reqBody)
	}()

	// // 第二个请求：Frame Rate Control
	// wg.Add(1)
	// go func() {
	// 	defer wg.Done()
	// 	reqBody := FrameRateRequest{
	// 		Cmd: "Frame Rate Control",
	// 		FPS: 8,
	// 	}
	// 	res2, err2 = postRequest(client, url, reqBody)
	// }()

	// 等待所有请求完成
	wg.Wait()

	// 处理第一个请求结果
	if err1 != nil {
		fmt.Println("设备音频请求失败:", err1)
		return err1
	} else {
		if res1.Code == 0 {
			fmt.Println("设备音频已开启")
		} else {
			fmt.Println("设备音频未开启")
		}
	}

	// // 处理第二个请求结果
	// if err2 != nil {
	// 	fmt.Println("设置视频帧率请求失败:", err2)
	// } else {
	// 	if res2.Code == 0 {
	// 		fmt.Println("设置视频帧率成功")
	// 	} else {
	// 		fmt.Println("设置视频帧率失败")
	// 	}
	// }

	return nil
}

func closeAudioServer(ip string) error {
	// 1. 定义设备IP
	url := fmt.Sprintf("http://%s:8000", ip)

	// 2. 创建带超时的 HTTP 客户端（10秒超时，对应 axios timeout）
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// 存储两个请求的结果
	var res1 Response
	//  var res2  Response
	// 存储错误信息
	var err1 error
	// var err2  error

	reqBody := VisualIntercomRequest{
		Cmd:  "set visual intercom",
		Open: false,
	}
	res1, err1 = postRequest(client, url, reqBody)

	// 处理第一个请求结果
	if err1 != nil {
		fmt.Println("设备音频请求失败:", err1)
		return err1
	} else {
		if res1.Code == 0 {
			fmt.Println("设备音频已关闭")
		} else {
			fmt.Println("设备音频未关闭")
		}
	}

	// }

	return nil
}

// postRequest 封装POST JSON请求工具函数
func postRequest(client *http.Client, url string, reqBody any) (Response, error) {
	// 序列化请求体为JSON
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return Response{}, err
	}

	// 创建请求
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return Response{}, err
	}

	// 设置请求头（必须，对应axios默认行为）
	req.Header.Set("Content-Type", "application/json")

	// 发送请求
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()

	// 解析响应JSON
	var response Response
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return Response{}, err
	}

	return response, nil
}

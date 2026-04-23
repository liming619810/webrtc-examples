package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// 配置
const (
	wsHost     = "192.168.88.51"
	wsAddr     = "ws://192.168.88.51:8001" // WebSocket 目标地址
	oggFile    = "output.ogg"              // 你的 ogg 文件
	pushPeriod = 20 * time.Millisecond     // 实时语音分片发送（20ms 标准）
	frameSize  = 160                       // 8000Hz + 20ms = 160 字节/G711
)

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
func PushG711ToWebSocket(conn *websocket.Conn, stream io.Reader) error {
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

func main() {
	openAudioServer(wsHost)
	// 1. 连接 WebSocket
	fmt.Println("🔌 连接 WebSocket:", wsAddr)
	conn, _, err := websocket.DefaultDialer.Dial(wsAddr, nil)
	if err != nil {
		panic("WebSocket 连接失败: " + err.Error())
	}
	defer conn.Close()

	// 2. 打开 G711 实时流
	g711Stream, err := OggToG711Stream(oggFile)
	if err != nil {
		panic("打开流失败: " + err.Error())
	}
	defer g711Stream.Close()

	// 3. 开始实时推送
	fmt.Println("🚀 开始推送 G711 音频流...")
	err = PushG711ToWebSocket(conn, g711Stream)
	if err != nil {
		panic("推送失败: " + err.Error())
	}

}

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

package main

// import (
// 	"bytes"
// 	"encoding/json"
// 	"fmt"
// 	"net/http"
// 	"sync"
// 	"time"
// )

// // 定义请求体结构体（对应 JS 中的 JSON 参数）
// type VisualIntercomRequest struct {
// 	Cmd  string `json:"cmd"`
// 	Open bool   `json:"open"`
// }

// type FrameRateRequest struct {
// 	Cmd string `json:"cmd"`
// 	FPS int    `json:"fps"`
// }

// // 定义通用响应结构体（根据你的返回格式定义）
// type Response struct {
// 	Code int `json:"code"`
// 	// 可以根据实际返回补充其他字段
// }

// func openAudioServer(ip string) error {
// 	// 1. 定义设备IP
// 	url := fmt.Sprintf("http://%s:8000", ip)

// 	// 2. 创建带超时的 HTTP 客户端（10秒超时，对应 axios timeout）
// 	client := &http.Client{
// 		Timeout: 10 * time.Second,
// 	}

// 	// 3. 定义并发等待组
// 	var wg sync.WaitGroup
// 	// 存储两个请求的结果
// 	var res1 Response
// 	//  var res2  Response
// 	// 存储错误信息
// 	var err1 error
// 	// var err2  error

// 	// ==================== 并发发送两个POST请求 ====================
// 	// 第一个请求：set visual intercom
// 	wg.Add(1)
// 	go func() {
// 		defer wg.Done()
// 		reqBody := VisualIntercomRequest{
// 			Cmd:  "set visual intercom",
// 			Open: true,
// 		}
// 		res1, err1 = postRequest(client, url, reqBody)
// 	}()

// 	// // 第二个请求：Frame Rate Control
// 	// wg.Add(1)
// 	// go func() {
// 	// 	defer wg.Done()
// 	// 	reqBody := FrameRateRequest{
// 	// 		Cmd: "Frame Rate Control",
// 	// 		FPS: 8,
// 	// 	}
// 	// 	res2, err2 = postRequest(client, url, reqBody)
// 	// }()

// 	// 等待所有请求完成
// 	wg.Wait()

// 	// 处理第一个请求结果
// 	if err1 != nil {
// 		fmt.Println("设备音频请求失败:", err1)
// 		return err1
// 	} else {
// 		if res1.Code == 0 {
// 			fmt.Println("设备音频已开启")
// 		} else {
// 			fmt.Println("设备音频未开启")
// 		}
// 	}

// 	// // 处理第二个请求结果
// 	// if err2 != nil {
// 	// 	fmt.Println("设置视频帧率请求失败:", err2)
// 	// } else {
// 	// 	if res2.Code == 0 {
// 	// 		fmt.Println("设置视频帧率成功")
// 	// 	} else {
// 	// 		fmt.Println("设置视频帧率失败")
// 	// 	}
// 	// }

// 	return nil
// }

// func closeAudioServer(ip string) error {
// 	// 1. 定义设备IP
// 	url := fmt.Sprintf("http://%s:8000", ip)

// 	// 2. 创建带超时的 HTTP 客户端（10秒超时，对应 axios timeout）
// 	client := &http.Client{
// 		Timeout: 10 * time.Second,
// 	}

// 	// 存储两个请求的结果
// 	var res1 Response
// 	//  var res2  Response
// 	// 存储错误信息
// 	var err1 error
// 	// var err2  error

// 	reqBody := VisualIntercomRequest{
// 		Cmd:  "set visual intercom",
// 		Open: false,
// 	}
// 	res1, err1 = postRequest(client, url, reqBody)

// 	// 处理第一个请求结果
// 	if err1 != nil {
// 		fmt.Println("设备音频请求失败:", err1)
// 		return err1
// 	} else {
// 		if res1.Code == 0 {
// 			fmt.Println("设备音频已关闭")
// 		} else {
// 			fmt.Println("设备音频未关闭")
// 		}
// 	}

// 	// }

// 	return nil
// }

// // postRequest 封装POST JSON请求工具函数
// func postRequest(client *http.Client, url string, reqBody any) (Response, error) {
// 	// 序列化请求体为JSON
// 	jsonData, err := json.Marshal(reqBody)
// 	if err != nil {
// 		return Response{}, err
// 	}

// 	// 创建请求
// 	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
// 	if err != nil {
// 		return Response{}, err
// 	}

// 	// 设置请求头（必须，对应axios默认行为）
// 	req.Header.Set("Content-Type", "application/json")

// 	// 发送请求
// 	resp, err := client.Do(req)
// 	if err != nil {
// 		return Response{}, err
// 	}
// 	defer resp.Body.Close()

// 	// 解析响应JSON
// 	var response Response
// 	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
// 		return Response{}, err
// 	}

// 	return response, nil
// }

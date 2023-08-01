package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	addr, mode, refText, lang, appKey, secretKey string
	sleep                                        bool
	packetLen                                    int
)

/*
Simulate the real usage scenario, the user evaluates the websocket streaming mode while talking,
and the final package quickly responds to the evaluation results
*/
func main() {
	// Required parameters
	flag.StringVar(&addr, "addr", "", "api host e.g. xxxxx.speech-eval.com") // The specific address will be explained in the AppKey email
	flag.StringVar(&appKey, "appKey", "", "your appKey —— uuid format")      // The specific appKey will be explained in the AppKey email
	flag.StringVar(&secretKey, "appSecret", "", "your secret")               // The specific secret will be explained in the AppKey email

	// Optional parameters
	flag.BoolVar(&sleep, "sleep", true, "whether to sleep during sending audio data")
	flag.IntVar(&packetLen, "pl", 7680, "each audio packet length")

	flag.Parse()
	log.SetFlags(0)
	fmt.Printf("Your AppKey:%s,Your AppSecret:%s,Your addr:%s \n", appKey, secretKey, addr)

	// Identify your needs
	lang = "en-US"          // e.g. english eval
	mode = "word"           // e.g. assessment question type
	refText = "supermarket" // e.g. supermarket.wav

	doEval()
}

func getSignature(timestamp int64) string {
	s := fmt.Sprintf("appId=%s&timestamp=%d", appKey, timestamp)
	mac := hmac.New(sha1.New, []byte(secretKey))
	mac.Write([]byte(s))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func getToken() string {
	url := fmt.Sprintf("https://%s/auth/generateToken", addr)

	config := map[string]interface{}{}
	timestamp := time.Now().Unix()
	config["signature"] = getSignature(timestamp)
	config["appKey"] = appKey
	config["timestamp"] = timestamp

	data, _ := json.Marshal(config)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(data))
	if err != nil {
		log.Fatal("get token dial:", err)
	}

	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()

	if gjson.GetBytes(body, "code").String() != "00000" {
		log.Fatal("get token body:", string(body))
	}
	return gjson.GetBytes(body, "data.token").String()
}

func doEval() {
	header := http.Header{}
	header.Add("Authorization", fmt.Sprintf("Bearer %s", getToken()))

	// connect request
	conn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("wss://%s/%s/%s", addr, lang, mode), header)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer conn.Close()

	// send start request
	evalId := sendStartMsg(conn)
	fmt.Println(evalId)

	wg := new(sync.WaitGroup)
	wg.Add(2)

	// get started and result message
	go readMessage(wg, conn)

	// send audio data
	go sendMessage(wg, conn)
	wg.Wait()
}

func sendStartMsg(conn *websocket.Conn) string {
	start := map[string]interface{}{
		"common": map[string]interface{}{
			"api": "speecheval",
			"cmd": "start",
		},
	}

	start["payload"] = map[string]interface{}{
		"params": map[string]string{
			"refText": refText,
			"mode":    mode,
		},
		"langType":       lang,
		"attachAudioUrl": true,
		"format":         "wav", // e.g ./supermarket.wav
		"sampleRate":     16000,
	}
	_ = conn.WriteJSON(start)

	// get started message
	messageType, message, err := conn.ReadMessage()
	if err != nil {
		log.Fatal("read started message err:", err)
	}

	if messageType != websocket.TextMessage || gjson.GetBytes(message, "ack").String() != "started" {
		log.Fatalf("message type:%d,\tmessage:%s\n", messageType, message)
	}
	return gjson.GetBytes(message, "evalId").String()
}

func readMessage(wg *sync.WaitGroup, conn *websocket.Conn) {
	defer wg.Done()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("ReadMessageErr:%v \n", err)
		}

		switch gjson.GetBytes(message, "ack").String() {
		case "warning":
			log.Printf("EvaluationWarning --> message:%s\n", message)
		case "error":
			log.Printf("EvaluationError --> message:%s\n", message)
		case "result":
			if gjson.GetBytes(message, "eof").Int() == 1 {
				// get final result
			} else {
				// real-time result
			}
			log.Printf(" Successfully fetched results ===> %s", message)
		default: // completed
			return
		}
	}
}

func sendMessage(wg *sync.WaitGroup, conn *websocket.Conn) {
	defer wg.Done()

	f, err := os.Open("./supermarket.wav")
	if err != nil {
		log.Printf("OpenFile Err:%v \n", err)
	}
	defer f.Close()

	buf := make([]byte, packetLen)
	for {
		n, err := f.Read(buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if sleep {
			time.Sleep(240 * time.Millisecond)
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
			fmt.Println("write message:", err)
			return
		}
	}

	conn.WriteJSON(map[string]interface{}{"common": map[string]string{"cmd": "stop", "api": "speecheval"}})

}

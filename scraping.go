package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ant0ine/go-json-rest/rest"
)

//////////////////////////////////////////////////////////////////////////////////////
// 定数
//////////////////////////////////////////////////////////////////////////////////////

//TimeLayout 時刻フォーマット
const TimeLayout = "2006/01/02 15:04:05"

//AllSpot 全スポット
const AllSpot = "1,2,3,5,6,4,10,12,7,8"

//////////////////////////////////////////////////////////////////////////////////////
// 変数
//////////////////////////////////////////////////////////////////////////////////////

//lock 排他制御
var lock = sync.RWMutex{}

//lastExcuted 最終実行時刻
var lastExcuted int64

//lastRecovered 最終リカバリ時刻
var lastRecovered int64

//Httpでもらう設定値
var UserID string
var Password string
var SendAddress string
var AreaIdString string
var ApiCert string

//SessionID セッション
var SessionID string

//client HTTPリクエストクライアント（使いまわした方がいいらしいのでグローバル化）
var client *http.Client

//////////////////////////////////////////////////////////////////////////////////////
// 構造体
//////////////////////////////////////////////////////////////////////////////////////

//SpotInfo スクレイピング結果を格納する構造体
type SpotInfo struct {
	Time                              time.Time
	Area, Spot, Count, Name, Lat, Lon string
}

//JSpotinfo JSONマーシャリング構造体
type JSpotinfo struct {
	Spotinfo []InnerSpotinfo `json:"spotinfo"`
}

//InnerSpotinfo 台数情報
type InnerSpotinfo struct {
	Time  string `json:"time"`
	Area  string `json:"area"`
	Spot  string `json:"spot"`
	Count string `json:"count"`
}

//JSpotmaster JSONマーシャリング構造体
type JSpotmaster struct {
	Spotmaster []InnerSpotmaster `json:"spotmaster"`
}

//InnerSpotmaster スポット情報
type InnerSpotmaster struct {
	Area string `json:"area"`
	Spot string `json:"spot"`
	Name string `json:"name"`
	Lat  string `json:"lat"`
	Lon  string `json:"lon"`
}

//////////////////////////////////////////////////////////////////////////////////////
// レシーバ
//////////////////////////////////////////////////////////////////////////////////////

//Add SpotInfo構造体をJSON用にパースして加える
func (s *JSpotinfo) Add(time time.Time, area string, spot string, count string) {
	s.Spotinfo = append(s.Spotinfo, InnerSpotinfo{Time: time.Format(TimeLayout), Area: area, Spot: spot, Count: count})
}

//Size SpotInfo構造体のサイズを返す
func (s *JSpotinfo) Size() int {
	return len(s.Spotinfo)
}

//Add SpotInfo構造体をJSON用にパースして加える
func (s *JSpotmaster) Add(area string, spot string, name string, lat string, lon string) {
	s.Spotmaster = append(s.Spotmaster, InnerSpotmaster{Area: area, Spot: spot, Name: name, Lat: lat, Lon: lon})
}

//Size SpotInfo構造体のサイズを返す
func (s *JSpotmaster) Size() int {
	return len(s.Spotmaster)
}

//////////////////////////////////////////////////////////////////////////////////////
// 関数
//////////////////////////////////////////////////////////////////////////////////////

//GetSessionID ログインしてセッションIDを取得する
func GetSessionID() (string, error) {
	//リクエストBody作成
	values := url.Values{}
	values.Set("EventNo", "21401")
	values.Add("GarblePrevention", "ＰＯＳＴデータ")
	values.Add("MemberID", UserID)
	values.Add("Password", Password)
	values.Add("MemAreaID", "1")

	req, err := http.NewRequest(
		"POST",
		"https://tcc.docomo-cycle.jp/cycle/TYO/cs_web_main.php",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		fmt.Println("[Error]GetSessionID create NewRequest failed", err)
		return "", err
	}

	// リクエストHead作成
	ContentLength := strconv.FormatInt(req.ContentLength, 10)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Accept-Language", "ja,en-US;q=0.9,en;q=0.8,pt;q=0.7")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Content-Length", ContentLength)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Host", "tcc.docomo-cycle.jp")
	req.Header.Set("Origin", "https://tcc.docomo-cycle.jp")
	req.Header.Set("Referer", "https://tcc.docomo-cycle.jp/cycle/TYO/cs_web_main.php")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/80.0.3987.106 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("[Error]GetSessionID client.Do failed", err)
		return "", err
	}
	defer resp.Body.Close()

	doc, e := goquery.NewDocumentFromResponse(resp)
	if e != nil {
		fmt.Println("[Error]GetSessionID NewDocumentFromResponse failed", e)
		return "", e
	}

	SessionID, success := doc.Find("input[name='SessionID']").Attr("value")
	if !success {
		fmt.Println("[Error]GetSessionID Find SessionID failed")
		return "", fmt.Errorf("error")
	} else {
		fmt.Println("GetSessionID success ", SessionID)
		//成功したら待ち時間（1回目の検索に失敗するため）
		time.Sleep(3 * time.Second)
		return SessionID, nil
	}
}

//GetSpotInfoMain スクレイピングメイン関数
func GetSpotInfoMain(AreaID string, retry bool) ([]SpotInfo, error) {
	fmt.Printf("GetSpotInfoMain_start AreaID = %s \n", AreaID)
	var list []SpotInfo
	//リクエストBody作成
	values := url.Values{}
	values.Set("EventNo", "25706")
	values.Add("SessionID", SessionID)
	values.Add("UserID", "TYO")
	values.Add("MemberID", UserID)
	values.Add("GetInfoNum", "200")
	values.Add("GetInfoTopNum", "1")
	values.Add("MapType", "1")
	values.Add("MapCenterLat", "")
	values.Add("MapCenterLon", "")
	values.Add("MapZoom", "13")
	values.Add("EntServiceID", "TYO0001")
	values.Add("Location", "")
	values.Add("AreaID", AreaID)

	req, err := http.NewRequest(
		"POST",
		"https://tcc.docomo-cycle.jp/cycle/TYO/cs_web_main.php",
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		fmt.Println("[Error]GetSpotInfoMain create NewRequest failed", err)
		return nil, err
	}

	// リクエストHead作成
	ContentLength := strconv.FormatInt(req.ContentLength, 10)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Accept-Language", "ja,en-US;q=0.9,en;q=0.8,pt;q=0.7")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Content-Length", ContentLength)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Host", "tcc.docomo-cycle.jp")
	req.Header.Set("Origin", "https://tcc.docomo-cycle.jp")
	req.Header.Set("Referer", "https://tcc.docomo-cycle.jp/cycle/TYO/cs_web_main.php")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/80.0.3987.106 Safari/537.36")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("[Error]GetSpotInfoMain client.Do failed", err.Error())
		return nil, err
	}
	defer resp.Body.Close()

	doc, e := goquery.NewDocumentFromResponse(resp)
	if e != nil {
		fmt.Println("[Error]GetSpotInfoMain NewDocumentFromResponse failed", e)
		return nil, e
	}

	//エラーならログインし直して再チャレンジ
	if err := CheckErrorPage(doc); err != nil {
		if retry {
			SessionID, err = GetSessionID()
			if err != nil {
				return nil, err
			}
			//再帰呼び出し（次はリトライしない）
			return GetSpotInfoMain(AreaID, false)
		} else {
			//２回目は諦める
			return nil, err
		}
	}

	//スポットリスト解析
	doc.Find("form[name^=tab_]").Each(func(i int, s *goquery.Selection) {
		spotinfo := SpotInfo{Time: time.Now()}
		html, _ := s.Find("a").Html()
		err := ParseSpotInfoByText(html, &spotinfo)
		if err != nil {
			//メンテナンス中のスポットのエラーログは出力しない
			if strings.Index(err.Error(), "not cyclespot") < 0 {
				fmt.Println("[Error]GetSpotInfoMain ParseSpotInfoByText failed", err)
			}
			return
		}
		if val, exist := s.Find("input[name=ParkingLat]").Attr("value"); exist {
			spotinfo.Lat = val
		}
		if val, exist := s.Find("input[name=ParkingLon]").Attr("value"); exist {
			spotinfo.Lon = val
		}
		list = append(list, spotinfo)
	})

	fmt.Printf("GetSpotInfoMain_end AreaID = %s (%d件)\n", AreaID, len(list))
	return list, nil
}

//ParseSpotInfoByText テキスト解析
// "H1-43.東京イースト21<br/>H1-43.Tokyo East 21<br/>13台"の形式のテキストからarea,spot,name,countを取得する
func ParseSpotInfoByText(text string, s *SpotInfo) error {
	var codeAndName, cycleCount string
	if arr := strings.Split(text, "<br/>"); len(arr) == 3 {
		codeAndName = arr[0]
		cycleCount = arr[2]
	} else {
		return fmt.Errorf("[Error]ParseSpotInfoByText unexpected html : " + text)
	}

	// "H1-43"の部分
	indexDot := strings.Index(codeAndName, ".")
	if indexDot < 0 {
		return fmt.Errorf("[Error]ParseSpotInfoByText not cyclespot : " + text)
	}
	code := codeAndName[:indexDot]
	if arr := strings.Split(code, "-"); len(arr) == 2 {
		s.Area = arr[0]
		s.Spot = arr[1]
	} else {
		return fmt.Errorf("[Error]ParseSpotInfoByText unexpected code : " + text)
	}

	//名前
	s.Name = codeAndName[indexDot+1:]
	//台数
	if _, err := strconv.Atoi(cycleCount[:len(cycleCount)-3]); err == nil {
		s.Count = cycleCount[:len(cycleCount)-3]
	} else {
		return fmt.Errorf("[Error]ParseSpotInfoByText count not obtained : " + text)
	}

	//データサイズチェック
	if len(s.Area) > 3 || len(s.Spot) > 3 || len(s.Count) > 3 {
		fmt.Println("[Error]ParseSpotInfoByText data size obver : " + text)
	}

	return nil
}

//RegAllSpotInfo 全スポット登録関数
func RegAllSpotInfo() (err error) {
	//ロックする
	lock.Lock()
	defer lock.Unlock()
	//特に指定してない場合は全スポット
	if AreaIdString == "" {
		AreaIdString = AllSpot
	}
	fmt.Println("RegAllSpotInfo_Start AreaIdString =", AreaIdString)
	AreaIDs := strings.Split(AreaIdString, ",")
	for _, AreaID := range AreaIDs {
		if AreaID == "" {
			continue
		}
		//待ち時間いれる
		time.Sleep(5 * time.Second)
		//台数取得
		var list []SpotInfo
		list, err = GetSpotInfoMain(AreaID, true)
		if err != nil {
			fmt.Println("[Error]RegAllSpotInfo GetSpotInfoMain failed AreaID =", AreaID, err)
			continue
		}
		//負荷緩和のため100件ずつ送信
		max := 100
		jsondata := JSpotinfo{}
		for _, s := range list {
			jsondata.Add(s.Time, s.Area, s.Spot, s.Count)
			if jsondata.Size() >= max {
				SendSpotInfo(jsondata, false)
				jsondata = JSpotinfo{}
				time.Sleep(1 * time.Second)
			}
		}
		if jsondata.Size() >= 1 {
			SendSpotInfo(jsondata, false)
		}
	}
	fmt.Println("RegAllSpotInfo_End")
	return nil
}

//RegAllSpotMaster 全スポット登録関数（マスタメンテナンス）
func RegAllSpotMaster() (err error) {
	fmt.Println("RegAllSpotMaster_Start")
	//マスタメンテでは全スポットを対象とする
	AreaIDs := strings.Split(AllSpot, ",")
	for _, AreaID := range AreaIDs {
		//待ち時間いれる
		time.Sleep(5 * time.Second)
		//台数取得
		var list []SpotInfo
		list, err = GetSpotInfoMain(AreaID, true)
		if err != nil {
			fmt.Println("[Error]RegAllSpotMaster GetSpotInfoMain failed AreaID =", AreaID, err)
			continue
		}
		//負荷緩和のため100件ずつ送信
		max := 100
		jsondata := JSpotmaster{}
		for _, s := range list {
			jsondata.Add(s.Area, s.Spot, s.Name, s.Lat, s.Lon)
			if jsondata.Size() >= max {
				SendSpotMaster(jsondata)
				jsondata = JSpotmaster{}
				time.Sleep(1 * time.Second)
			}
		}
		if jsondata.Size() >= 1 {
			SendSpotMaster(jsondata)
		}
	}
	fmt.Println("RegAllSpotMaster_End")
	return nil
}

//CheckErrorPage エラーページかをチェックする
func CheckErrorPage(doc *goquery.Document) error {
	if title := doc.Find(".tittle_h1").Text(); strings.Index(title, "エラー") > -1 {
		fmt.Println(title)
		return fmt.Errorf(strings.TrimSpace(doc.Find(".main_inner_message").Text()))
	}
	return nil
}

//SendSpotInfo DBに送信する。JSONファイルからのリカバリの場合は失敗したらJSONを保存しないフラグ（第２引数）
func SendSpotInfo(jsonStruct JSpotinfo, fromRecovery bool) error {
	marshalized, _ := json.Marshal(jsonStruct)
	req, err := http.NewRequest(
		"POST",
		SendAddress,
		bytes.NewBuffer(marshalized),
	)
	if err != nil {
		fmt.Println("[Error]SendSpotInfo create NewRequest failed", err.Error())
		return err
	}

	// リクエストHead作成
	ContentLength := strconv.FormatInt(req.ContentLength, 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", ContentLength)
	req.Header.Set("cert", ApiCert)

	//送信
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("[Error]SendSpotInfo client.Do failed", err.Error())
		if !fromRecovery {
			SaveJSON(jsonStruct)
		}
		return err
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Println("[Error]SendSpotInfo StatusCode is not OK", resp.StatusCode, resp.Body)
		if !fromRecovery {
			SaveJSON(jsonStruct)
		}
		return fmt.Errorf("StatusCode is not OK : %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	return nil
}

//SendSpotMaster マスタ情報をDBに送信する。
func SendSpotMaster(jsonStruct JSpotmaster) error {
	marshalized, _ := json.Marshal(jsonStruct)
	req, err := http.NewRequest(
		"POST",
		SendAddress,
		bytes.NewBuffer(marshalized),
	)
	if err != nil {
		fmt.Println("[Error]SendSpotMaster create NewRequest failed", err.Error())
		return err
	}

	// リクエストHead作成
	ContentLength := strconv.FormatInt(req.ContentLength, 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", ContentLength)
	req.Header.Set("cert", ApiCert)

	//送信
	resp, err := client.Do(req)
	if err != nil {
		fmt.Println("[Error]SendSpotMaster client.Do failed", err.Error())
		return err
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Println("[Error]SendSpotMaster StatusCode is not OK", resp.StatusCode, resp.Body)
		return fmt.Errorf("StatusCode is not OK : %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	return nil
}

//TestGetSpotInfoMain 単体テスト
func TestGetSpotInfoMain(html string) ([]SpotInfo, error) {
	var list []SpotInfo
	// ファイルからドキュメントを作成（テスト用）
	f, e := os.Open(html)
	if e != nil {
		log.Fatal(e)
	}
	defer f.Close()

	doc, e := goquery.NewDocumentFromReader(f)
	if e != nil {
		log.Fatal(e)
	}

	//スポットリスト解析
	doc.Find("form[name^=tab_]").Each(func(i int, s *goquery.Selection) {
		spotinfo := SpotInfo{Time: time.Now()}
		html, _ := s.Find("a").Html()
		err := ParseSpotInfoByText(html, &spotinfo)
		if err != nil {
			return
		}
		if val, exist := s.Find("input[name=ParkingLat]").Attr("value"); exist {
			spotinfo.Lat = val
		}
		if val, exist := s.Find("input[name=ParkingLon]").Attr("value"); exist {
			spotinfo.Lon = val
		}
		list = append(list, spotinfo)
	})

	return list, nil

}

//PrepareScrayping スクレイピング準備（返り値がtrueの場合は実行しない）
func PrepareScrayping(w rest.ResponseWriter, r *rest.Request) (cancel bool) {
	//2分以内の連続実行を禁止する
	if now := time.Now().Unix(); now-lastExcuted < 120 {
		fmt.Println("2分以内に連続でリクエストされたためキャンセルしました。")
		w.WriteHeader(http.StatusOK)
		w.WriteJson("scraping canceled")
		return true
	}
	lastExcuted = time.Now().Unix()
	//パラメータ解析
	r.ParseForm()
	params := r.Form
	SendAddress = params.Get("address")
	if params.Get("id") == "" || params.Get("password") == "" || SendAddress == "" {
		w.WriteHeader(http.StatusForbidden)
		w.WriteJson("[ERROR] lack of parameter")
		return true
	}
	AreaIdString = params.Get("areaID")
	if env := params.Get("env"); env != "" {
		os.Setenv("API_CERT", env)
	}
	//引数で渡さなくても環境変数にある場合がある
	if val := os.Getenv("API_CERT"); val != "" {
		ApiCert = val
	}
	//セッションIDを使いまわす
	var err error
	if SessionID == "" {
		//空なら取得
		UserID = params.Get("id")
		Password = params.Get("password")
		SessionID, err = GetSessionID()
		if err != nil {
			fmt.Println("[Error]Start GetSessionID failed", err)
			w.WriteHeader(http.StatusBadRequest)
			w.WriteJson("login failed")
			return true
		}
	} else {
		if UserID != params.Get("id") || Password != params.Get("password") {
			//前回ログイン情報と異なる場合はログインし直し
			UserID = params.Get("id")
			Password = params.Get("password")
			SessionID, err = GetSessionID()
			if err != nil {
				fmt.Println("[Error]Start GetSessionID failed", err)
				w.WriteHeader(http.StatusBadRequest)
				w.WriteJson("login failed")
				return true
			}
		}
	}
	return false
}

//Start スクレイピング開始
func Start(w rest.ResponseWriter, r *rest.Request) {
	//チェック＆初期化
	if cancel := PrepareScrayping(w, r); cancel {
		return
	}
	//スクレイピング実行（非同期）
	go RegAllSpotInfo()
	//先にOKを返しておく
	w.WriteHeader(http.StatusOK)
	w.WriteJson("OK")
}

//StartMaster スクレイピング開始
func StartMaster(w rest.ResponseWriter, r *rest.Request) {
	//チェック＆初期化
	if cancel := PrepareScrayping(w, r); cancel {
		return
	}
	//スクレイピング実行（非同期）
	go RegAllSpotMaster()
	//先にOKを返しておく
	w.WriteHeader(http.StatusOK)
	w.WriteJson("OK")
}

//InitClient クライアント初期化
func InitClient() {
	//SSL証明書を無視したクライアント作成
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client = &http.Client{
		Transport: tr,
	}
}

//SaveJSON JSONファイルに保存する
func SaveJSON(jsonStruct JSpotinfo) error {
	filePath := strconv.FormatInt(time.Now().Unix(), 10) + "_save.json"
	if runtime.GOOS != "windows" {
		filePath = "/tmp/" + filePath
	}
	fp, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY, 0664)
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	defer fp.Close()

	e := json.NewEncoder(fp)
	if err := e.Encode(jsonStruct); err != nil {
		fmt.Println(err.Error())
		return err
	}
	return nil
}

//EnumTempFiles 一時ファイルを列挙する
func EnumTempFiles() (result []string) {
	pattern := "*_save.json"
	if runtime.GOOS != "windows" {
		pattern = "/tmp/" + pattern
	}
	if files, err := filepath.Glob(pattern); err != nil {
		fmt.Println("EnumTempFiles_Error :", err.Error())
	} else {
		result = files
	}
	return
}

//Recover JSONからリカバリー
func Recover(w rest.ResponseWriter, r *rest.Request) {
	//パラメータ解析
	r.ParseForm()
	params := r.Form
	//最大件数
	max := 5
	if param := params.Get("max"); param != "" {
		if val, err := strconv.Atoi(param); err == nil {
			max = val
		}
	}
	//max=0のときは件数確認のみのためチェックしない
	if max > 0 {
		if lastExcuted <= 0 {
			fmt.Println("未初期化のためキャンセルしました。")
			w.WriteHeader(http.StatusOK)
			w.WriteJson("recovery canceled")
			return
		}
		//2分以内の連続実行を禁止する
		if now := time.Now().Unix(); now-lastRecovered < 120 {
			fmt.Println("2分以内に連続でリクエストされたためキャンセルしました。")
			w.WriteHeader(http.StatusOK)
			w.WriteJson("recovery canceled")
			return
		}
		lastRecovered = time.Now().Unix()
	}

	//tmpファイルを列挙
	files := EnumTempFiles()
	if len(files) < 1 {
		fmt.Println("tmpにファイルがありません")
		w.WriteHeader(http.StatusOK)
		w.WriteJson("no recovery cache found")
		return
	} else if max == 0 {
		msg := fmt.Sprintf("%d files found : %v \n", len(files), files)
		fmt.Printf(msg)
		w.WriteHeader(http.StatusOK)
		w.WriteJson(msg)
		return
	}
	for i, filename := range files {
		if i >= max {
			break
		}
		path := filename
		if runtime.GOOS != "windows" {
			path = "/tmp/" + path
		}
		file, err := os.Open(path)
		if err != nil {
			msg := fmt.Sprintf("%s Open error : %v", path, err)
			fmt.Println(msg)
			w.WriteHeader(http.StatusOK)
			w.WriteJson(msg)
			return
		}
		defer file.Close()
		d := json.NewDecoder(file)
		d.DisallowUnknownFields() // エラーの場合 json: unknown field "JSONのフィールド名"
		var jsonstruct JSpotinfo
		if err := d.Decode(&jsonstruct); err != nil && err != io.EOF {
			msg := fmt.Sprintf("%s Decode error : %v", path, err)
			fmt.Println(msg)
			w.WriteHeader(http.StatusOK)
			w.WriteJson(msg)
			return
		}
		//DB登録処理
		if err := SendSpotInfo(jsonstruct, true); err != nil {
			//同じファイルで失敗し続けないようにしたいが何回かリトライのチャンスを与えたいのでMAX回数を引き上げる
			max++
			fmt.Printf("%s SendSpotInfo error : %v \n", path, err)
		} else {
			//成功したらファイル削除
			if err := os.Remove(path); err != nil {
				fmt.Printf("%s Remove error : %v \n", path, err)
				continue
			}
			fmt.Printf("%s Recover success \n", path)
		}
	}
}

// func main() {
// 	// list, _ := TestGetSpotInfoMain("./港.html")
// 	// SaveJSON(ConvertSpotinfoStruct(list))
// 	SendAddress = "http://localhost:5001/private/counts"

// }

func main() {
	api := rest.NewApi()
	api.Use(rest.DefaultDevStack...)
	router, err := rest.MakeRouter(
		rest.Get("/start", Start),
		rest.Get("/master", StartMaster),
		rest.Get("/recover", Recover),
	)
	if err != nil {
		log.Fatal(err)
	}

	if err != nil {
		log.Fatal(err)
	}
	api.SetApp(router)
	port := "5005"
	if val := os.Getenv("PORT"); val != "" {
		port = val
	}
	InitClient()
	log.Fatal(http.ListenAndServe(":"+port, api.MakeHandler()))
}

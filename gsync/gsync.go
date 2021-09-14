package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"time"

	simplejson "github.com/bitly/go-simplejson"
	"github.com/elgs/filesync/index"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Println("CPUs: ", runtime.NumCPU())

	input := args()
	done := make(chan bool)
	if len(input) >= 1 {
		start(input[0], done)
	}
	<-done
}

func start(configFile string, done chan bool) {
	b, err := ioutil.ReadFile(configFile)
	if err != nil {
		fmt.Println(configFile, " not found")
		go func() {
			done <- false
		}()
		return
	}
	json, _ := simplejson.NewJson(b)
	ip := json.Get("ip").MustString("127.0.0.1")
	port := json.Get("port").MustInt(6776)
	maxScanInterval := json.Get("maxScanInterval").MustString("5m")

	monitors := json.Get("monitors").MustMap()

	pubKeyFile := json.Get("pubKeyFile").MustString("public.key")
	pub, err := ioutil.ReadFile(pubKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	pubPem, _ := pem.Decode(pub)
	publicKey, err := x509.ParsePKCS1PublicKey(pubPem.Bytes)
	if err != nil {
		log.Fatal(err)
	}
	msi, _ := time.ParseDuration(maxScanInterval)

	for k, v := range monitors {
		monitored, _ := v.(string)
		go startWork(ip, port, k, monitored, msi, publicKey)
	}
}
func args() []string {
	ret := []string{}
	if len(os.Args) <= 1 {
		ret = append(ret, "gsync.json")
	} else {
		for i := 1; i < len(os.Args); i++ {
			ret = append(ret, os.Args[i])
		}
	}
	return ret
}

func startWork(ip string, port int, key string, monitored string, maxInterval time.Duration, publicKey *rsa.PublicKey) {
	var lastIndexed int64 = 0
	var changed bool = false
	sleepTime := time.Second
	for {
		if changed {
			sleepTime = time.Second
		} else {
			sleepTime *= 4
			if sleepTime >= maxInterval {
				sleepTime = maxInterval
			}
		}
		changed = false
		//fmt.Println("Sleep", sleepTime, lastIndexed)
		time.Sleep(sleepTime)
		dirs := dirsFromServer(ip, port, key, lastIndexed-3600, publicKey)
		if len(dirs) > 0 {
			for _, dir := range dirs {
				dirMap, _ := dir.(map[string]interface{})
				dirPath, _ := dirMap["FilePath"].(string)
				dirStatus := dirMap["Status"].(string)
				dir := index.PathSafe(index.SlashSuffix(monitored) + dirPath)
				if dirStatus == "deleted" {
					err := os.RemoveAll(dir)
					if err != nil {
						fmt.Println(err)
					}
					continue
				}
				mode, _ := dirMap["FileMode"].(json.Number)
				dirMode, _ := mode.Int64()
				err := os.MkdirAll(dir, os.FileMode(dirMode))
				if err != nil {
					fmt.Println(err)
				}
			}

			files := filesFromServer(ip, port, key, "/", lastIndexed-3600, publicKey)
			for _, file := range files {
				fileMap, _ := file.(map[string]interface{})
				filePath, _ := fileMap["FilePath"].(string)
				fileStatus := fileMap["Status"].(string)
				indexed, _ := fileMap["LastIndexed"].(json.Number)
				serverIndexed, _ := indexed.Int64()
				if serverIndexed > lastIndexed {
					lastIndexed = serverIndexed
				}

				f := index.PathSafe(index.SlashSuffix(monitored) + filePath)
				if fileStatus == "deleted" {
					err := os.RemoveAll(f)
					if err != nil {
						fmt.Println(err)
					}
					continue
				}
				size, _ := fileMap["FileSize"].(json.Number)
				fileSize, _ := size.Int64()
				if info, err := os.Stat(f); os.IsNotExist(err) {
					// file does not exists, download it
					changed = true
					func() {
						out, _ := os.Create(f)
						defer out.Close()
						downloadFromServer(ip, port, key, filePath, 0, fileSize, out, publicKey)
					}()
				} else {
					// file exists, analyze it
					modified, _ := fileMap["LastModified"].(json.Number)
					lastModified, _ := modified.Int64()
					if fileSize == info.Size() && lastModified < info.ModTime().Unix() {
						// this file is probably not changed
						continue
					}
					// file change, analyse it block by block
					changed = true
					fileParts := filePartsFromServer(ip, port, key, filePath, publicKey)
					func() {
						out, _ := os.OpenFile(f, os.O_RDWR, os.FileMode(0666))
						defer out.Close()
						out.Truncate(fileSize)
						if len(fileParts) == 0 {
							return
						}
						h := crc32.NewIEEE()
						for _, filePart := range fileParts {
							filePartMap, _ := filePart.(map[string]interface{})
							idx, _ := filePartMap["StartIndex"].(json.Number)
							startIndex, _ := idx.Int64()
							ost, _ := filePartMap["Offset"].(json.Number)
							offset, _ := ost.Int64()
							checksum := filePartMap["Checksum"].(string)

							buf := make([]byte, offset)
							n, _ := out.ReadAt(buf, startIndex)

							h.Reset()
							h.Write(buf[:n])
							v := fmt.Sprint(h.Sum32())
							if checksum == v {
								// block unchanged
								return
							}
							// block changed
							downloadFromServer(ip, port, key, filePath, startIndex, offset, out, publicKey)
						}
					}()
				}
			}
		}
	}
}

func downloadFromServer(ip string, port int, key string, filePath string, start int64, length int64, file *os.File, publicKey *rsa.PublicKey) int64 {
	defer func() {
		if err := recover(); err != nil {
			fmt.Println(err)
		}
	}()
	client := &http.Client{}
	query := fmt.Sprint("file_path=", url.QueryEscape(filePath), "&start=", start, "&length=", length)
	encryptedBytes, _ := rsa.EncryptOAEP(
		sha256.New(),
		rand.Reader,
		publicKey,
		[]byte(query),
		nil)
	encodedQuery := base64.URLEncoding.EncodeToString(encryptedBytes)
	req, _ := http.NewRequest("GET", fmt.Sprint("http://", ip, ":", port,
		"/download?query=", encodedQuery), nil)
	req.Header.Add("AUTH_KEY", key)
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	file.Seek(start, os.SEEK_SET)
	n, _ := io.CopyN(file, resp.Body, length)
	return n
}

func filePartsFromServer(ip string, port int, key string, filePath string, publicKey *rsa.PublicKey) []interface{} {
	defer func() {
		if err := recover(); err != nil {
			fmt.Println(err)
		}
	}()
	client := &http.Client{}
	query := fmt.Sprint("file_path=", url.QueryEscape(filePath))
	encryptedBytes, _ := rsa.EncryptOAEP(
		sha256.New(),
		rand.Reader,
		publicKey,
		[]byte(query),
		nil)
	encodedQuery := base64.URLEncoding.EncodeToString(encryptedBytes)
	req, _ := http.NewRequest("GET", fmt.Sprint("http://", ip, ":", port,
		"/file_parts?query=", encodedQuery), nil)
	req.Header.Add("AUTH_KEY", key)
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	json, _ := simplejson.NewJson(body)
	fileParts := json.MustArray()
	return fileParts
}

func filesFromServer(ip string, port int, key string, filePath string, lastIndexed int64, publicKey *rsa.PublicKey) []interface{} {
	defer func() {
		if err := recover(); err != nil {
			fmt.Println(err)
		}
	}()
	client := &http.Client{}
	query := fmt.Sprint("last_indexed=", lastIndexed, "&file_path=", url.QueryEscape(filePath))
	encryptedBytes, _ := rsa.EncryptOAEP(
		sha256.New(),
		rand.Reader,
		publicKey,
		[]byte(query),
		nil)
	encodedQuery := base64.URLEncoding.EncodeToString(encryptedBytes)
	req, _ := http.NewRequest("GET", fmt.Sprint("http://", ip, ":", port,
		"/files?query=", encodedQuery), nil)
	req.Header.Add("AUTH_KEY", key)
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	json, _ := simplejson.NewJson(body)
	files := json.MustArray()
	return files
}

func dirsFromServer(ip string, port int, key string, lastIndexed int64, publicKey *rsa.PublicKey) []interface{} {
	defer func() {
		if err := recover(); err != nil {
			fmt.Println(err)
		}
	}()
	client := &http.Client{}
	query := fmt.Sprint("last_indexed=", lastIndexed)
	encryptedBytes, _ := rsa.EncryptOAEP(
		sha256.New(),
		rand.Reader,
		publicKey,
		[]byte(query),
		nil)
	encodedQuery := base64.URLEncoding.EncodeToString(encryptedBytes)
	req, _ := http.NewRequest("GET", fmt.Sprint("http://", ip, ":", port, "/dirs?query=", encodedQuery), nil)
	req.Header.Add("AUTH_KEY", key)
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	json, _ := simplejson.NewJson(body)
	dirs := json.MustArray()
	return dirs
}

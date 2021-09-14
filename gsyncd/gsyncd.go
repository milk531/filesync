package main

import (
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"runtime"

	simplejson "github.com/bitly/go-simplejson"
	"github.com/elgs/filesync/api"
	"github.com/elgs/filesync/index"
	"github.com/fsnotify/fsnotify"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	fmt.Println("CPUs: ", runtime.NumCPU())

	input := args()
	if len(input) >= 1 {
		start(input[0])
	}
}

func start(configFile string) {
	b, err := ioutil.ReadFile(configFile)
	if err != nil {
		fmt.Println(configFile, " not found")
		return
	}
	json, _ := simplejson.NewJson(b)
	ip := json.Get("ip").MustString("127.0.0.1")
	port := json.Get("port").MustInt(6776)

	monitors := json.Get("monitors").MustMap()

	for _, v := range monitors {
		watcher, _ := fsnotify.NewWatcher()
		monitored, _ := v.(string)
		monitored = index.PathSafe(monitored)
		db, _ := sql.Open("sqlite3", index.SlashSuffix(monitored)+".sync/index.db")
		defer db.Close()
		db.Exec("VACUUM;")
		index.InitIndex(monitored, db)
		index.WatchRecursively(watcher, monitored, monitored)
		go index.ProcessEvent(watcher, monitored)
	}

	priKeyFile := json.Get("priKeyFile").MustString("private_key.pem")
	pri, err := ioutil.ReadFile(priKeyFile)
	if err != nil {
		log.Fatal(err)
	}
	priPem, _ := pem.Decode(pri)
	privateKey, err := x509.ParsePKCS1PrivateKey(priPem.Bytes)
	if err != nil {
		log.Fatal(err)
	}

	api.RunWeb(ip, port, monitors, privateKey)
	//watcher.Close()
}

func args() []string {
	ret := []string{}
	if len(os.Args) <= 1 {
		ret = append(ret, "gsyncd.json")
	} else {
		for i := 1; i < len(os.Args); i++ {
			ret = append(ret, os.Args[i])
		}
	}
	return ret
}

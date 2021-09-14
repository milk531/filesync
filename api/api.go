package api

import (
	"crypto"
	"crypto/rsa"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"github.com/codegangsta/martini"
	"github.com/codegangsta/martini-contrib/encoder"
	"github.com/elgs/filesync/index"
)

func RunWeb(ip string, port int, monitors map[string]interface{}, privateKey *rsa.PrivateKey) {
	m := martini.New()
	route := martini.NewRouter()

	// validate an api key
	m.Use(func(res http.ResponseWriter, req *http.Request) {
		authKey := req.Header.Get("AUTH_KEY")
		if monitors[authKey] == nil {
			res.WriteHeader(http.StatusUnauthorized)
			res.Write([]byte("Unauthorized access."))
		} else {
			monitored, _ := monitors[authKey].(string)
			req.Header.Set("MONITORED", monitored)
		}
	})
	// decode the query string
	m.Use(func(res http.ResponseWriter, req *http.Request) {
		rawStr := req.FormValue("query")
		encryptedBytes, err := base64.URLEncoding.DecodeString(rawStr)
		if err == nil {
			decryptedBytes, err := privateKey.Decrypt(nil, encryptedBytes, &rsa.OAEPOptions{Hash: crypto.SHA256})
			if err == nil {
				queryStr := string(decryptedBytes)
				queryForm, _ := url.ParseQuery(queryStr)
				for k, v := range queryForm {
					req.Form.Set(k, v[0])
				}
				return
			}
		}
		res.WriteHeader(http.StatusBadRequest)
		res.Write([]byte("Bad Request."))
	})

	// map json encoder
	m.Use(func(c martini.Context, w http.ResponseWriter) {
		c.MapTo(encoder.JsonEncoder{}, (*encoder.Encoder)(nil))
		w.Header().Set("Content-Type", "application/json")
	})

	route.Get("/dirs", func(enc encoder.Encoder, req *http.Request) (int, []byte) {
		defer func() {
			if err := recover(); err != nil {
				fmt.Println(err)
			}
		}()
		monitored := req.Header.Get("MONITORED")
		lastIndexed, _ := strconv.Atoi(req.FormValue("last_indexed"))
		result := make([]index.IndexedFile, 0)

		db, _ := sql.Open("sqlite3", index.SlashSuffix(monitored)+".sync/index.db")
		defer db.Close()
		psSelectDirs, _ := db.Prepare("SELECT * FROM FILES WHERE FILE_SIZE=-1 AND LAST_INDEXED>?")
		defer psSelectDirs.Close()
		rows, _ := psSelectDirs.Query(lastIndexed)
		defer rows.Close()
		for rows.Next() {
			file := new(index.IndexedFile)
			rows.Scan(&file.FilePath, &file.LastModified, &file.FileSize, &file.FileMode, &file.Status, &file.LastIndexed)
			result = append(result, *file)
		}
		return http.StatusOK, encoder.Must(enc.Encode(result))
	})

	route.Get("/files", func(enc encoder.Encoder, req *http.Request) (int, []byte) {
		monitored := req.Header.Get("MONITORED")
		lastIndexed, _ := strconv.Atoi(req.FormValue("last_indexed"))
		filePath := index.SlashSuffix(index.LikeSafe(req.FormValue("file_path")))
		result := make([]index.IndexedFile, 0)

		db, _ := sql.Open("sqlite3", index.SlashSuffix(monitored)+".sync/index.db")
		defer db.Close()
		psSelectFiles, _ := db.Prepare(`SELECT * FROM FILES
				WHERE LAST_INDEXED>? AND FILE_SIZE>=0 AND STATUS!='updating' AND FILE_PATH LIKE ?`)
		defer psSelectFiles.Close()
		rows, _ := psSelectFiles.Query(lastIndexed, filePath+"%")
		defer rows.Close()
		for rows.Next() {
			file := new(index.IndexedFile)
			rows.Scan(&file.FilePath, &file.LastModified, &file.FileSize, &file.FileMode, &file.Status, &file.LastIndexed)
			result = append(result, *file)
		}
		return http.StatusOK, encoder.Must(enc.Encode(result))
	})

	route.Get("/file_parts", func(enc encoder.Encoder, req *http.Request) (int, []byte) {
		monitored := req.Header.Get("MONITORED")
		filePath := req.FormValue("file_path")
		result := make([]index.IndexedFilePart, 0)

		db, _ := sql.Open("sqlite3", index.SlashSuffix(monitored)+".sync/index.db")
		defer db.Close()
		psSelectFiles, _ := db.Prepare(`SELECT * FROM FILE_PARTS
				WHERE FILE_PATH=? ORDER BY FILE_PATH,SEQ`)
		defer psSelectFiles.Close()
		rows, _ := psSelectFiles.Query(filePath)
		defer rows.Close()
		for rows.Next() {
			filePart := new(index.IndexedFilePart)
			rows.Scan(&filePart.FilePath, &filePart.Seq, &filePart.StartIndex, &filePart.Offset, &filePart.Checksum, &filePart.ChecksumType)
			result = append(result, *filePart)
		}
		return http.StatusOK, encoder.Must(enc.Encode(result))
	})

	route.Get("/download", func(res http.ResponseWriter, req *http.Request) {
		monitored := req.Header.Get("MONITORED")
		filePath := req.FormValue("file_path")
		start, _ := strconv.ParseInt(req.FormValue("start"), 10, 64)
		length, _ := strconv.ParseInt(req.FormValue("length"), 10, 64)

		file, _ := os.Open(index.SlashSuffix(monitored) + filePath)
		defer file.Close()
		file.Seek(start, os.SEEK_SET)
		n, _ := io.CopyN(res, file, length)
		res.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		res.Header().Set("Content-Type", "application/octet-stream")
	})

	m.Action(route.Handle)
	fmt.Println(http.ListenAndServe(fmt.Sprint(ip, ":", port), m))
}

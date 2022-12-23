package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ipfsFiles "github.com/ipfs/go-ipfs-files"
	httpapi "github.com/ipfs/go-ipfs-http-client"
	caopts "github.com/ipfs/interface-go-ipfs-core/options"
	ipfsPath "github.com/ipfs/interface-go-ipfs-core/path"
	flag "github.com/spf13/pflag"
)

const infuraAPI = "https://ipfs.infura.io:5001"
const MAX_CONCURRENT_JOBS = 8

type Metadata struct {
	Image string `json:"image"`
}

func fileNameWithoutExt(fileName string) string {
	return strings.TrimSuffix(fileName, filepath.Ext(fileName))
}

func main() {
	projectId := flag.String("id", "", "your Infura ProjectID")
	projectSecret := flag.String("secret", "", "your Infura ProjectSecret")
	api := flag.String("url", infuraAPI, "the API URL")
	pin := flag.Bool("pin", true, "whether or not to pin the data")
	urlPrefix := flag.String("prefix", "", "path to prepend to ipfs hash")
	jsonPath := flag.String("out", "", "where to save json files")

	flag.Parse()

	if *projectId == "" {
		_, _ = fmt.Fprintln(os.Stderr, "parameter --id is required")
		os.Exit(1)
	}
	if *projectSecret == "" {
		_, _ = fmt.Fprintln(os.Stderr, "parameter --secret is required")
		os.Exit(1)
	}

	httpClient := &http.Client{}
	client, err := httpapi.NewURLApiWithClient(*api, httpClient)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	client.Headers.Add("Authorization", "Basic "+basicAuth(*projectId, *projectSecret))

	args := flag.Args()
	if len(args) != 1 {
		_, _ = fmt.Fprintln(os.Stderr, "file or directory path required as an argument")
		os.Exit(1)
	}
	path := args[0]

	// trap Ctrl+C and call cancel on the context
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	defer func() {
		signal.Stop(c)
		cancel()
	}()
	go func() {
		select {
		case <-c:
			cancel()
		case <-ctx.Done():
		}
	}()

	start := time.Now()

	// List files in directory
	files, err := ioutil.ReadDir(path)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var a [3333]string
	var counter int64

	var wg sync.WaitGroup
	wg.Add(len(files))

	waitChan := make(chan struct{}, MAX_CONCURRENT_JOBS)

	for _, file := range files {
		waitChan <- struct{}{}

		go func(file fs.FileInfo) {
			if !file.IsDir() {
				fullPath := filepath.Join(path, file.Name())

				stat, err := os.Lstat(fullPath)
				if err != nil {
					_, _ = fmt.Fprintln(os.Stderr, err)
					wg.Done()
					<-waitChan
					return
				}

				fileName := stat.Name()
				shortFileName := fileNameWithoutExt(fileName)
				index, err := strconv.Atoi(shortFileName)
				if err != nil {
					_, _ = fmt.Fprintln(os.Stderr, err)
					wg.Done()
					<-waitChan
					return
				}

				ipfsFile, err := ipfsFiles.NewSerialFile(fullPath, false, stat)
				if err != nil {
					_, _ = fmt.Fprintln(os.Stderr, err)
					wg.Done()
					<-waitChan
					return
				}

				var res ipfsPath.Resolved
				res, err = client.Unixfs().Add(ctx, ipfsFile, caopts.Unixfs.Pin(*pin), caopts.Unixfs.Progress(true))

				if err != nil {
					_, _ = fmt.Fprintln(os.Stderr, err)
					wg.Done()
					<-waitChan
					return
				}

				cid := res.Cid().String()
				a[index - 1] = cid

				if *jsonPath != "" {
					data := Metadata{
						Image: *urlPrefix + cid,
					}
					jsonFile, _ := json.MarshalIndent(data, "", "  ")
					_ = ioutil.WriteFile(filepath.Join(*jsonPath, shortFileName + ".json"), jsonFile, 0644)
				}

				count := atomic.AddInt64(&counter, 1)

				_, _ = fmt.Fprintln(os.Stdout, count, index, cid)
			}

			wg.Done()
			<-waitChan
		}(file)
	}

	wg.Wait()

	exit(start, 0)
}

func exit(start time.Time, exitCode int) {
	duration := time.Since(start)
	_, _ = fmt.Fprintln(os.Stderr, duration)
	os.Exit(exitCode)
}

func basicAuth(projectId, projectSecret string) string {
	auth := projectId + ":" + projectSecret
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

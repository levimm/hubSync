package main

import (
	"fmt"
	"net/http"
	"io/ioutil"
	"encoding/json"
	"strings"
	"sync"
	"io"
	"docker.io/go-docker"
	"context"
	"docker.io/go-docker/api/types"
	"os"
	"bytes"
	log "github.com/sirupsen/logrus"
	"time"
	"math"
)

const (
	DOCKER_HUB_REPO_LIST string = "https://index.docker.io/v1/search?q=%s&n=%d&page=%d"
	DOCKER_HUB_TAG_LIST string = "https://registry.hub.docker.com/v1/repositories/%s/tags"
	DOCKER_HUB_TAG_PAGE string = "https://store.docker.com/api/content/v1/repositories/public/library/%s/tags?page_size=%d&page=%d"
	DOCKER_STORE_DETAILS string = "https://store.docker.com/api/content/v1/products/images/%s"
)

type Repo struct {
	Name 		string	`json:"name"`
	NameSpace 	string  `json:"name_space,omitempty"`
	Description	string	`json:"description"`
	StarCount	int		`json:"star_count"`
	IsTrusted	bool	`json:"is_trusted"`
	IsAutomated	bool	`json:"is_automated"`
	IsOfficial	bool	`json:"is_official"`
}

type QueryResponse struct {
	NumPages	int		`json:"num_pages"`
	NumResults	int		`json:"num_results"`
	PageSize	int		`json:"page_size"`
	PageIndex	int		`json:"page"`
	Query 		string	`json:"query"`
	Results 	[]Repo	`json:"results"`
}

func getAllRepos(namespace string) (repos []Repo) {
	pageSize := 100
	pageIndex := 1
	repoQueue := make(chan Repo, pageSize)
	totalPages := getPagedRepos(namespace, pageSize, pageIndex, repoQueue)
	log.Infof("Total candidate pages: %d\n", totalPages)
	pageIndex++

	var wg sync.WaitGroup
	wg.Add(totalPages-1)
	for ;pageIndex <= totalPages; pageIndex++ {
		go func(i int){
			defer wg.Done()
			getPagedRepos(namespace, pageSize, i, repoQueue)
		}(pageIndex)
	}

	go func() {
		wg.Wait()
		close(repoQueue)
	}()

	for repo := range repoQueue {
		repos = append(repos, repo)
	}

	return
}

func getPagedRepos(namespace string, pageSize, pageIndex int, repoChan chan<- Repo) (pages int){
	log.Infof("Getting page %d with %d records per page\n", pageIndex, pageSize)
	url := fmt.Sprintf(DOCKER_HUB_REPO_LIST, namespace, pageSize, pageIndex)
	client := &http.Client{}
	resp, err := client.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	var data QueryResponse
	err = json.Unmarshal(body, &data)
	if err != nil {
		panic(err)
	}
	pages = data.NumPages
	results := data.Results
	for _, re := range results {
		if strings.Index(re.Name, "/") == -1 && re.IsOfficial{
			re.NameSpace = "library"
			repoChan <- re
		}
	}

	return
}

func getPagedTags(repoName string, pageSize, pageIndex int, tags *[]string) (pages int){
	log.Infof("Getting page %d with %d records per page\n", pageIndex, pageSize)
	url := fmt.Sprintf(DOCKER_HUB_TAG_PAGE, repoName, pageSize, pageIndex)
	client := &http.Client{}
	resp, err := client.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	var data map[string]interface{}
	err = json.Unmarshal(body, &data)
	if err != nil {
		panic(err)
	}

	pages = int(math.Ceil(data["count"].(float64) / float64(pageSize)))

	results := data["results"].([]interface{})

	for _, result := range results {
		re := result.(map[string]interface{})
		name := re["name"].(string)
		if strings.Contains(name, "windowsserver") || strings.Contains(name, "nanoserver") {
			continue
		}
		*tags = append(*tags, name)
	}

	return
}

func getAllTags(repoName string) (tags []string) {
	log.Infof("Getting tag list for %s\n", repoName)
	url := fmt.Sprintf(DOCKER_HUB_TAG_LIST, repoName)
	client := &http.Client{}
	resp, err := client.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	var data []map[string]string
	err = json.Unmarshal(body, &data)
	if err != nil {
		panic(err)
	}
	for _, d := range data {
		tags = append(tags, d["name"])
	}

	return
}

func getFirst100Tags(repoName string) (tags []string) {
	log.Infof("Getting tag list for %s\n", repoName)
	pageSize := 100
	pageIndex := 1

	pageNum := getPagedTags(repoName, pageSize, pageIndex, &tags)

	for pageIndex := 2; pageIndex <= pageNum; pageIndex++ {
		getPagedTags(repoName, pageSize, pageIndex, &tags)
	}
	if len(tags) > 100 {
		tags = tags[:100]
	}

	return
}

func listAllTags(cli *docker.Client, ctx context.Context) (result []string) {
	repos := getAllRepos("library")
	for _, repo := range repos {
		tags := getAllTags(repo.Name)
		for _, tag := range tags {
			result = append(result, repo.Name+":"+tag)
		}
	}
	return
}

func listAllRepos() (result []string) {
	repos := getAllRepos("library")
	for _, repo := range repos {
		result = append(result, repo.Name)
	}
	return
}

func check(all, images, skips []string) (remain []string, extra []string) {
	for _, item := range all {
		if contains(images, item) {
			continue
		}
		if contains(skips, item) {
			continue
		}
		remain = append(remain, item)
	}

	return
}

func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func pullOfficialImages(cli *docker.Client, ctx context.Context, tags []string) (imageNames []string, skipNames []string) {
	for _, tag := range tags {
		retryCount := 0
		log.Infof("Pulling %s\n", tag)

		var out io.ReadCloser
		var err error
		for {
			out, err = cli.ImagePull(ctx, tag, types.ImagePullOptions{})
			if err != nil {
				// retry if timeout happens
				if retryCount > 5 {
					log.WithFields(log.Fields{
						"image": tag,
						"retry": retryCount,
					}).Error(err)
					break
				}
				if strings.Contains(err.Error(), "TLS handshake timeout") {
					log.WithFields(log.Fields{
						"image": tag,
						"retry": retryCount,
					}).Error("Timeout to pull image, wait for 5 seconds and retry")
					time.Sleep(5 * time.Second)
					retryCount++
				} else {
					log.WithFields(log.Fields{
						"image": tag,
						"retry": retryCount,
					}).Error(err)
					time.Sleep(5 * time.Second)
					retryCount++
				}
			} else {
				break
			}
		}

		if retryCount > 5 {
			log.WithFields(log.Fields{
				"image": tag,
			}).Error("exceed retry count, skip this one")
			skipNames = append(skipNames, tag)
			continue
		}

		buf := new(bytes.Buffer)
		buf.ReadFrom(out)
		out.Close()
		if bytes.Contains(buf.Bytes(), []byte("no matching manifest for linux/amd64 in the manifest list entries")) {
			log.WithFields(log.Fields{
				"image": tag,
			}).Info("no matching manifest for linux/amd64, skip pulling")
			skipNames = append(skipNames, tag)
			continue
		}
		if bytes.Contains(buf.Bytes(), []byte("Image is up to date for")) ||
			bytes.Contains(buf.Bytes(), []byte("Downloaded newer image for")) {
			imageNames = append(imageNames, tag)
		} else {
			skipNames = append(skipNames, tag)
		}

		io.Copy(os.Stdout, buf)
	}

	return
}

func getDescription(repoName string) (short, full string) {
	log.Infof("Getting detail info for %s\n", repoName)
	url := fmt.Sprintf(DOCKER_STORE_DETAILS, repoName)
	client := &http.Client{}
	resp, err := client.Get(url)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	var data map[string]interface{}
	err = json.Unmarshal(body, &data)
	if err != nil {
		panic(err)
	}
	short = data["short_description"].(string)
	full = data["full_description"].(string)

	return
}
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
)

const (
	DOCKER_HUB_REPO_LIST string = "https://index.docker.io/v1/search?q=%s&n=%d&page=%d"
	DOCKER_HUB_TAG_LIST string = "https://registry.hub.docker.com/v1/repositories/%s/tags"
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
	fmt.Printf("Total candidate pages: %d\n", totalPages)
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
	fmt.Printf("Getting page %d with %d records per page\n", pageIndex, pageSize)
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

func getAllTags(repoName string) (tags []string) {
	fmt.Printf("Getting tag list for %s\n", repoName)
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

func pullAndRetag(cli *docker.Client, ctx context.Context) (repoNames []string, imageNames []string) {
	repos := getAllRepos("library")
	fmt.Printf("Total number of repos to sync: %d\n", len(repos))

	var wg sync.WaitGroup
	wg.Add(len(repos))
	for _, repo := range repos{
		repoNames = append(repoNames, repo.Name)

		go func(repoName string) {
			defer wg.Done()
			tags := getAllTags(repoName)
			fmt.Printf("Repo %s has %d tags.\n", repoName, len(tags))

			for _, tag := range tags {
				imageName := repoName + ":" + tag
				fmt.Printf("Pulling %s\n", imageName)
				out, err := cli.ImagePull(ctx, imageName, types.ImagePullOptions{})
				if err != nil {
					panic(err)
				}
				buf := new(bytes.Buffer)
				buf.ReadFrom(out)
				out.Close()
				if bytes.Contains(buf.Bytes(), []byte("no matching manifest for linux/amd64 in the manifest list entries")) {
					continue
				}

				io.Copy(os.Stdout, buf)

				// retag the image
				newTag := "reg.qiniu.com/mali/" + imageName
				imageNames = append(imageNames, newTag)
				err = cli.ImageTag(ctx, imageName, newTag)
				if err != nil {
					panic(err)
				}
			}
		}(repo.Name)
	}

	wg.Wait()
	return
}

func getDescription(repoName string) (short, full string) {
	fmt.Printf("Getting detail info for %s\n", repoName)
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
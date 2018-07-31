package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"docker.io/go-docker/api/types/mount"
	log "github.com/sirupsen/logrus"
	_ "gopkg.in/mgo.v2"
	_ "gopkg.in/mgo.v2/bson"
)

func main() {
	cli, err := docker.NewEnvClient()
	ctx := context.Background()
	if err != nil {
		log.Error(cli, ctx)
		panic(err)
	}

	//
	//initSync()
	//pullSync(cli, ctx, false)
	//pushSync(cli, ctx, false)

	// this is the docker image hub-sync-pull-push code
	//pullSync(cli, ctx, true)
	//pushSync(cli, ctx, true, false)

	// this is the golang executable ./runsync code
	//imagePtr := flag.String("image", "reg.qiniu.com/mali/hub-sync-pull-push:always-false", "specify the image to be managed by this app")
	//concurrentNumPtr := flag.Int("concurrent", 5, "the most concurrent dind container number")
	//flag.Parse()
	//reposToUpdate := flag.Args()
	//
	//runSync(cli, ctx, *imagePtr, *concurrentNumPtr, reposToUpdate)

	// this is the golang executable that checks status
	//checkSync()

	// some test

	// Step3: modify stark db.repos.summary, db.repos.description
	//repos := []string{"alpine"}
	//for _, repo := range repos {
	//	short, full := getDescription(repo)
	//	session, err := mgo.Dial("mongodb://10.34.42.52:7088/hms")
	//	if err != nil {
	//		panic(err)
	//	}
	//	c := session.DB("hms").C("repos")
	//	repoFilter := bson.M{"namespace": "library", "name": repo}
	//	err = c.Update(repoFilter, bson.M{"$set": bson.M{"summary": short, "description": full}})
	//	if err != nil {
	//		panic(err)
	//	}
	//}
}

// initSync is the executable that initialize directory struct for dind
func initSync() {
	const repoListFile = "./repos.txt"

	// check if repos.txt exist and examine the last modified time
	repoNeedUpdate := false
	repoFile, err := os.Stat(repoListFile)
	var repoList []string
	if err != nil {
		repoNeedUpdate = true
	} else {
		since := time.Since(repoFile.ModTime())
		if since.Hours() > 24 {
			repoNeedUpdate = true
		}
	}
	if repoNeedUpdate {
		log.Println("Fetching latest repo list.")
		repoList = listAllRepos()
		writeToFile(repoListFile, repoList)
	} else {
		repoList = getFromFile(repoListFile)
		log.Println("Repo list is already latest, no need to update.")
	}

	// mkdir for all repos and save tags.txt
	for _, repo := range repoList {
		mountPath := path.Join("./repos", repo, "docker")
		os.MkdirAll(mountPath, 0777)
		datPath := path.Join("./repos", repo, "dat")
		os.MkdirAll(datPath, 0777)

		tagNeedUpdate := false
		tagListFile := path.Join(datPath, "tags.txt")
		tagFile, err := os.Stat(tagListFile)
		if err != nil {
			tagNeedUpdate = true
		} else {
			since := time.Since(tagFile.ModTime())
			if since.Hours() > 24 {
				tagNeedUpdate = true
			}
		}
		if tagNeedUpdate {
			log.Printf("Fetching latest 100 tags for repo %s", repo)
			tagList := getFirst100Tags(repo)
			for i := range tagList {
				tagList[i] = repo + ":" + tagList[i]
			}
			writeToFile(tagListFile, tagList)
		} else {
			log.Printf("%s's tag list is already latest, no need to update.", repo)
		}
	}
}

// pullSync is the pull part for dind image
func pullSync(cli *docker.Client, ctx context.Context, always bool) {
	tags := getFromFile("/dat/tags.txt")
	var toDownload []string
	repoName := strings.Split(tags[0], ":")[0]

	if always == false {
		images, err := cli.ImageList(ctx, types.ImageListOptions{All: true})
		if err != nil {
			log.WithFields(log.Fields{
				"repo": repoName,
			}).Error("Cannot get image list", err)
		}

		var downloadTags []string
		for _, image := range images {
			for _, repoTag := range image.RepoTags {
				if strings.Contains(repoTag, "reg.qiniu.com") == false {
					downloadTags = append(downloadTags, repoTag)
				}
			}
		}
		for _, tag := range tags {
			if contains(downloadTags, tag) == false {
				toDownload = append(toDownload, tag)
			}
		}
	} else {
		toDownload = tags
	}

	if len(toDownload) > 0 {
		log.Printf("still need to download tags: %v", toDownload)
		pullOfficialImages(cli, ctx, toDownload)
		log.Printf("Finish pulling repo %s", repoName)
	} else {
		log.Printf("Already pulled repo %s, jump to next", repoName)
	}
}

// runSync is the executable that manages all running containers
func runSync(cli *docker.Client, ctx context.Context, imageParam string, concurrentParam int, reposParam []string) {
	repos, err := ioutil.ReadDir("./repos")
	if err != nil {
		log.Fatal(err)
	}

	var reposToUpdate []string
	if len(reposParam) > 0 {
		reposToUpdate = reposParam
	} else {
		for _, repo := range repos {
			reposToUpdate = append(reposToUpdate, repo.Name())
		}
	}

	// concurrent control
	ch := make(chan struct{}, concurrentParam)
	var wg sync.WaitGroup
	wg.Add(len(reposToUpdate))

	for _, repo := range reposToUpdate {
		go func(repoName string) {
			defer wg.Done()
			ch <- struct{}{}
			log.Printf("Creating dind container to sync repo %s", repoName)

			resp, err := cli.ContainerCreate(ctx, &container.Config{
				Image: imageParam,
			}, &container.HostConfig{
				AutoRemove: false,
				Privileged: true,
				Mounts: []mount.Mount{
					{
						Type:   mount.TypeBind,
						Source: path.Join("/home/mali/hubSync/repos", repoName, "docker"),
						Target: "/var/lib/docker",
					},
					{
						Type:   mount.TypeBind,
						Source: path.Join("/home/mali/hubSync/repos", repoName, "dat"),
						Target: "/dat",
					},
				},
			}, nil, "")
			if err != nil {
				panic(err)
			}
			if err := cli.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
				panic(err)
			}

			statusCh, errCh := cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
			select {
			case err := <-errCh:
				if err != nil {
					panic(err)
				}
			case <-statusCh:
			}

			out, err := cli.ContainerLogs(ctx, resp.ID, types.ContainerLogsOptions{ShowStdout: true})
			if err != nil {
				panic(err)
			}

			io.Copy(os.Stdout, out)

			cli.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{})

			<-ch
		}(repo)
	}

	wg.Wait()
	log.Println("All Finished!")
}

// pushSyncMy is the push part for dind image that pushes to my registry namespace
func pushSync(cli *docker.Client, ctx context.Context, self bool) {
	tags := getFromFile("/dat/tags.txt")
	repoName := strings.Split(tags[0], ":")[0]
	var toPush []string

	images, err := cli.ImageList(ctx, types.ImageListOptions{All: true})
	if err != nil {
		log.WithFields(log.Fields{
			"repo": repoName,
		}).Error("Cannot get image list", err)
	}
	var downloadTags []string
	for _, image := range images {
		for _, repoTag := range image.RepoTags {
			downloadTags = append(downloadTags, repoTag)
		}
	}
	for _, tag := range tags {
		if contains(downloadTags, tag) == true {
			toPush = append(toPush, tag)
		}
	}

	if len(toPush) > 0 {
		var pushes, skips []string
		if self {
			pushes, skips = pushToMyRegistry(cli, ctx, toPush)
		} else {
			pushes, skips = pushToOfficialRegistry(cli, ctx, toPush)
		}
		writeToFile("/dat/push_skips.txt", skips)
		writeToFile("/dat/push_success.txt", pushes)
		log.Printf("Finish pushing repo %s", repoName)
	} else {
		log.Printf("Already pushed repo %s, jump to next", repoName)
	}
}

func pauseForCheck(step int) {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		log.Infof("Finished phase %d, type yes if you want to continue", step)
		scanner.Scan()
		text := scanner.Text()
		if text == "yes" {
			break
		}
	}
}

func checkSync(cli *docker.Client, ctx context.Context) {
	tags := getFromFile("/dat/tags.txt")
	repoName := strings.Split(tags[0], ":")[0]
	var leftTags []string

	images, err := cli.ImageList(ctx, types.ImageListOptions{All: true})
	if err != nil {
		log.WithFields(log.Fields{
			"repo": repoName,
		}).Error("Cannot get image list", err)
	}
	var downloadTags []string
	for _, image := range images {
		for _, repoTag := range image.RepoTags {
			if strings.Contains(repoTag, "reg.qiniu.com") == false {
				downloadTags = append(downloadTags, repoTag)
			}
		}
	}
	for _, tag := range tags {
		if contains(downloadTags, tag) == false {
			leftTags = append(leftTags, tag)
		}
	}
	var output string
	fmt.Sprintf(output, "Repo %s has left tags %v", repoName, leftTags)
	os.Stdout.WriteString(output)
}

func writeToFile(path string, strs []string) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	for _, s := range strs {
		tmp := s + "\n"
		_, err = f.WriteString(tmp)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func getFromFile(path string) (result []string) {
	file, err := os.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		result = append(result, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	return
}

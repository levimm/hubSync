package main

import (
	"docker.io/go-docker"
	"context"
	"os"
	"bufio"
	_ "gopkg.in/mgo.v2"
	_ "gopkg.in/mgo.v2/bson"
	log "github.com/sirupsen/logrus"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/container"
	"time"
	"path"
	"io"
	"io/ioutil"
	"docker.io/go-docker/api/types/mount"
	"sync"
	"strings"
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
	pullSync(cli, ctx, false)

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
			toDownload = tags
		}

		for _, image := range images {
			downloadTags := image.RepoTags
			for _, tag :=range tags {
				if contains(downloadTags, tag) == false {
					toDownload = append(toDownload, tag)
				}
			}
		}
	} else {
		toDownload = tags
	}

	if len(toDownload) > 0 {
		pullOfficialImages(cli, ctx, toDownload)

		images, err := cli.ImageList(ctx, types.ImageListOptions{All: true})
		if err != nil {
			log.WithFields(log.Fields{
				"repo": repoName,
			}).Error("Cannot get image list", err)
		}
		var officialTags []string
		for _, image := range images {
			downloadTags := image.RepoTags
			for _, t := range downloadTags {
				if strings.Contains(t, "reg.qiniu.com") {
					continue
				}
				officialTags = append(officialTags, t)
			}
			writeToFile("/dat/downloads.txt", officialTags)
		}
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

			<- ch
		}(repo)
	}

	wg.Wait()
	log.Println("All Finished!")
}

// pushSyncMy is the push part for dind image that pushes to my registry namespace
func pushSync(cli *docker.Client, ctx context.Context, always bool, self bool) {
	tags := getFromFile("/dat/tags.txt")
	pushNeeded := true
	repoName := strings.Split(tags[0], ":")[0]
	downloads := getFromFile("/dat/downloads.txt")

	// check if no need to push
	if always == false {
		_, err := os.Stat("/dat/push_success.txt")
		if err == nil {
			pushes := getFromFile("/dat/push_success.txt")
			skips := getFromFile("/dat/push_skips.txt")
			if len(pushes)+len(skips) == len(downloads) {
				pushNeeded = false
			} else {
				log.Printf("Num is not correct when pushing, please check repo %s", repoName)
			}
		}
	}

	if pushNeeded {
		var pushes, skips []string
		if self {
			pushes, skips = pushToMyRegistry(cli, ctx, downloads)
		} else {
			pushes, skips = pushToOfficialRegistry(cli, ctx, downloads)
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

func checkSync() {
	repos, err := ioutil.ReadDir("./repos")
	if err != nil {
		log.Fatal(err)
	}
	for _, repoDir := range repos {
		repoName := repoDir.Name()
		tagsPath := path.Join("/home/mali/hubSync/repos", repoName, "dat/tags.txt")
		tags := getFromFile(tagsPath)
		pullDownloadsPath := path.Join("/home/mali/hubSync/repos", repoName, "dat/downloads.txt")
		pullSkipsPath := path.Join("/home/mali/hubSync/repos", repoName, "dat/skips.txt")
		pushSuccessPath := path.Join("/home/mali/hubSync/repos", repoName, "dat/push_success.txt")
		pushSkipsPath := path.Join("/home/mali/hubSync/repos", repoName, "dat/push_skips.txt")

		_, err1 := os.Stat(pullDownloadsPath)
		_, err2 := os.Stat(pullSkipsPath)
		if err1 == nil && err2 == nil {
			downloads := getFromFile(pullDownloadsPath)
			skips := getFromFile(pullSkipsPath)
			log.Printf("Pull complete for repo %s, skipped tags %v", repoName, skips)
			if len(tags) == len(downloads)+len(skips) {
				log.Printf("All tags is pulled.\n")
			} else {
				var left []string
				for _, tag := range tags {
					if contains(downloads, tag) {
						continue
					}
					if contains(skips, tag) {
						continue
					}

					left = append(left, tag)
				}
				log.Printf("Not all tags is pulled. Left tags %v.\n", left)
			}
		}

		_, err1 = os.Stat(pushSuccessPath)
		_, err2 = os.Stat(pushSkipsPath)
		if err1 == nil && err2 == nil {
			downloads := getFromFile(pullDownloadsPath)
			pushes := getFromFile(pushSuccessPath)
			skips := getFromFile(pushSkipsPath)
			log.Printf("Push complete for repo %s, skipped tags %v", repoName, skips)
			if len(downloads) == len(pushes)+len(skips) {
				log.Printf("All tags is pushed.\n")
			} else {
				var left []string
				for _, tag := range downloads {
					if contains(pushes, tag) {
						continue
					}
					if contains(skips, tag) {
						continue
					}
					left = append(left, tag)
				}
				log.Printf("Not all tags is pulled. Left tags %v.\n", left)
			}
		}
	}
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
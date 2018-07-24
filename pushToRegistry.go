package main

import (
	"encoding/base64"
	"io"
	"docker.io/go-docker/api/types"
	"encoding/json"
	"os"
	"docker.io/go-docker"
	"context"
	log "github.com/sirupsen/logrus"
	"strings"
)

func listImages(cli *docker.Client, ctx context.Context) (images []string) {
	// list images
	imgs, err := cli.ImageList(ctx, types.ImageListOptions{All: true})
	if err != nil {
		log.Fatal(err)
	}

	for _, i := range imgs {
		for _, t := range i.RepoTags {
			if strings.Contains(t, "<none>") {
				continue
			}
			if strings.Contains(t, "mali") {
				continue
			}
			images = append(images, t)
		}
	}

	return
}

func pushToMyRegistry(cli *docker.Client, ctx context.Context, images []string) (pushes, skips []string){
	log.Infof("Continuing on next phase: push to reg.qiniu.com/mali")

	authConfig := types.AuthConfig{
		Username: "mali@qiniu.com",
		Password: "mali1312",
	}
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		panic(err)
	}
	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	for _, image := range images {
		// retag the image
		newTag := "reg.qiniu.com/mali/" + image
		err = cli.ImageTag(ctx, image, newTag)
		if err != nil {
			log.WithFields(log.Fields{
				"before retag": image,
				"after retag": newTag,
			}).Error("Fail to retag. ", err)
			skips = append(skips, image)
			continue
		}

		log.WithFields(log.Fields{
			"tag": newTag,
		}).Infoln("Pushing tag.")

		retryCount := 0
		var out io.ReadCloser
		for {
			out, err = cli.ImagePush(ctx, newTag, types.ImagePushOptions{RegistryAuth: authStr})
			if err != nil {
				if retryCount > 5 {
					log.WithFields(log.Fields{
						"tag": newTag,
					}).Error("Fail to push image. ", err)
					break
				}
				retryCount++
			} else {
				break
			}
		}
		if retryCount > 5 {
			skips = append(skips, newTag)
			continue
		}
		pushes = append(pushes, newTag)
		io.Copy(os.Stdout, out)
		out.Close()
	}

	return
}


func pushToOfficialRegistry(cli *docker.Client, ctx context.Context, images []string) (pushes, skips []string) {
	log.Infof("Continuing on next phase: push to reg.qiniu.com/library")

	authConfig := types.AuthConfig{
		Username: "kirk-internal@qiniu.com",
		Password: "00381730-6055-11e7-98a5-e3b030c07506",
	}
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		panic(err)
	}
	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	for _, image := range images {
		// retag the image
		newTag := "reg.qiniu.com/library/" + image
		err = cli.ImageTag(ctx, image, newTag)
		if err != nil {
			log.WithFields(log.Fields{
				"before retag": image,
				"after retag": newTag,
			}).Error("Fail to retag. ", err)
			skips = append(skips, image)
			continue
		}

		log.WithFields(log.Fields{
			"tag": newTag,
		}).Infoln("Pushing tag.")

		retryCount := 0
		var out io.ReadCloser
		for {
			out, err = cli.ImagePush(ctx, newTag, types.ImagePushOptions{RegistryAuth: authStr})
			if err != nil {
				if retryCount > 5 {
					log.WithFields(log.Fields{
						"tag": newTag,
					}).Error("Fail to push image. ", err)
					break
				}
				retryCount++
			} else {
				break
			}
		}
		if retryCount > 5 {
			skips = append(skips, newTag)
			continue
		}
		pushes = append(pushes, newTag)
		io.Copy(os.Stdout, out)
		out.Close()
	}

	return
}
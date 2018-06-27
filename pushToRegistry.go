package main

import (
	"fmt"
	"encoding/base64"
	"io"
	"docker.io/go-docker/api/types"
	"encoding/json"
	"os"
	"docker.io/go-docker"
	"context"
	"sync"
)

func pushToRegistry(cli *docker.Client, ctx context.Context, images []string) {
	fmt.Println("Continuing on next phase: push to reg.qiniu.com")
	authConfig := types.AuthConfig{
		Username: "",
		Password: "",
	}
	encodedJSON, err := json.Marshal(authConfig)
	if err != nil {
		panic(err)
	}
	authStr := base64.URLEncoding.EncodeToString(encodedJSON)

	var wg sync.WaitGroup
	wg.Add(len(images))
	for _, img := range images {
		go func(image string) {
			defer wg.Done()
			out, err := cli.ImagePush(ctx, image, types.ImagePushOptions{RegistryAuth: authStr})
			if err != nil {
				panic(err)
			}

			io.Copy(os.Stdout, out)
			out.Close()
		}(img)
	}
	wg.Wait()
}
package main

import (
	"docker.io/go-docker"
	"context"
	"os"
	"bufio"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	"deploy/logstash/gopath/src/ilog"
)

func main() {
	cli, err := docker.NewEnvClient()
	ctx := context.Background()
	if err != nil {
		panic(err)
	}

	// Step1: pull repos from docker hub
	repos, images := pullAndRetag(cli, ctx)
	pauseForCheck(1)

	// Step2: push to qiniu registry
	pushToRegistry(cli, ctx, images)
	pauseForCheck(2)


	// Step3: modify stark db.repos.summary, db.repos.description
	for _, repo := range repos {
		short, full := getDescription(repo)
		session, err := mgo.Dial("mongodb://127.0.0.1:27017/hms")
		if err != nil {
			panic(err)
		}
		c := session.DB("hms").C("repos")
		repoFilter := bson.M{"namespace": "mali", "name": repo}
		err = c.Update(repoFilter, bson.M{"$set": bson.M{"summary": short, "description": full}})
		if err != nil {
			panic(err)
		}
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
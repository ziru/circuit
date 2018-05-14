package dockerbuilder

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codeamp/circuit/plugins"
	log "github.com/codeamp/logger"
	"github.com/codeamp/transistor"
	"github.com/extemporalgenome/slug"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/spf13/viper"
)

type DockerBuilder struct {
	events chan transistor.Event
	event  transistor.Event
	Socket string
}

func init() {
	transistor.RegisterPlugin("dockerbuilder", func() transistor.Plugin {
		return &DockerBuilder{Socket: "unix:///var/run/docker.sock"}
	})
}

func (x *DockerBuilder) Description() string {
	return "Clone git repository and build a docker image"
}

func (x *DockerBuilder) SampleConfig() string {
	return ` `
}

func (x *DockerBuilder) Start(e chan transistor.Event) error {
	x.events = e
	log.Info("Started DockerBuilder")
	// create global gitconfig file
	err := ioutil.WriteFile("/root/.gitconfig", []byte("[user]\n  name = codeamp \n  email = codeamp@codeamp.com"), 0600)
	if err != nil {
		log.Debug(err)
		return err
	}
	
	return nil
}

func (x *DockerBuilder) Stop() {
	log.Println("Stopping DockerBuilder")
}

func (x *DockerBuilder) Subscribe() []string {
	return []string{
		"plugins.ReleaseExtension:create:dockerbuilder",
		"plugins.ReleaseExtension:update:dockerbuilder",
		"plugins.ProjectExtension:create:dockerbuilder",
		"plugins.ProjectExtension:update:dockerbuilder",
	}
}

func (x *DockerBuilder) git(env []string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)

	log.InfoWithFields("executing command", log.Fields{
		"path": cmd.Path,
		"args": strings.Join(cmd.Args, " "),
	})

	cmd.Env = env

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.Error); ok {
			if ee.Err == exec.ErrNotFound {
				return nil, errors.New("Git executable not found in $PATH")
			}
		}

		return nil, errors.New(string(bytes.TrimSpace(out)))
	}

	return out, nil
}

func (x *DockerBuilder) bootstrap(repoPath string, event plugins.ReleaseExtension) error {
	var err error
	var output []byte

	// idRsaPath := fmt.Sprintf("%s/%s_id_rsa", event.Release.Git.Workdir, event.Release.Project.Repository)
	idRsaPath := fmt.Sprintf("%s/%s_id_rsa", viper.GetString("plugins.dockerbuilder.workdir"), event.Release.Project.Repository)
	repoPath = fmt.Sprintf("%s/%s_%s", viper.GetString("plugins.dockerbuilder.workdir"), event.Release.Project.Repository, event.Release.Git.Branch)
	idRsa := fmt.Sprintf("GIT_SSH_COMMAND=ssh -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no -i %s -F /dev/null", idRsaPath)

	// Git Env
	env := os.Environ()
	env = append(env, idRsa)

	_, err = exec.Command("mkdir", "-p", filepath.Dir(repoPath)).CombinedOutput()
	if err != nil {
		return err
	}

	if _, err := os.Stat(idRsaPath); os.IsNotExist(err) {
		log.InfoWithFields("creating repository id_rsa", log.Fields{
			"path": idRsaPath,
		})

		err := ioutil.WriteFile(idRsaPath, []byte(event.Release.Git.RsaPrivateKey), 0600)
		if err != nil {
			log.Debug(err)
			return err
		}
	}

	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		log.InfoWithFields("cloning repository", log.Fields{
			"path": repoPath,
		})

		output, err := x.git(env, "clone", event.Release.Git.Url, repoPath)
		if err != nil {
			log.Debug(err)
			return err
		}
		log.Info(string(output))
	}

	output, err = x.git(env, "-C", repoPath, "checkout", event.Release.Git.Branch)
	if err != nil {
		log.Info(err)
		return err
	}
	log.Info(string(output))

	output, err = x.git(env, "-C", repoPath, "pull", "origin", event.Release.Git.Branch)
	if err != nil {
		log.Info(err)
		return err
	}
	log.Info(string(output))

	return nil
}

func (x *DockerBuilder) build(repoPath string, event plugins.ReleaseExtension, dockerBuildOut io.Writer) error {
	repoPath = fmt.Sprintf("%s/%s_%s", viper.GetString("plugins.dockerbuilder.workdir"), event.Release.Project.Repository, event.Release.Git.Branch)
	gitArchive := exec.Command("git", "archive", event.Release.HeadFeature.Hash)
	gitArchive.Dir = repoPath
	gitArchiveOut, err := gitArchive.StdoutPipe()
	if err != nil {
		log.Debug(err)
		return err
	}

	gitArchiveErr, err := gitArchive.StderrPipe()
	if err != nil {
		log.Debug(err)
		return err
	}

	err = gitArchive.Start()
	if err != nil {
		log.Fatal(err)
		return err
	}

	dockerBuildIn := bytes.NewBuffer(nil)
	go func() {
		io.Copy(os.Stderr, gitArchiveErr)
	}()

	io.Copy(dockerBuildIn, gitArchiveOut)

	err = gitArchive.Wait()
	if err != nil {
		log.Debug(err)
		return err
	}

	buildArgs := []docker.BuildArg{}
	for _, secret := range event.Release.Secrets {
		if secret.Type == plugins.GetType("build") {
			ba := docker.BuildArg{
				Name:  secret.Key,
				Value: secret.Value,
			}
			buildArgs = append(buildArgs, ba)
		}
	}
	fullImagePath := fullImagePath(x.event)
	buildOptions := docker.BuildImageOptions{
		Dockerfile:   fmt.Sprintf("Dockerfile"),
		Name:         fullImagePath,
		OutputStream: dockerBuildOut,
		InputStream:  dockerBuildIn,
		BuildArgs:    buildArgs,
	}

	dockerClient, err := docker.NewClient(x.Socket)
	if err != nil {
		log.Debug(err)
		return err
	}

	err = dockerClient.BuildImage(buildOptions)
	if err != nil {
		log.Debug(err)
		return err
	}

	return nil
}

func (x *DockerBuilder) push(repoPath string, event plugins.ReleaseExtension, buildlog io.Writer) error {
	var err error

	buildlog.Write([]byte(fmt.Sprintf("Pushing %s\n", imagePathGen(x.event))))

	user, err := x.event.GetArtifact("user")
	if err != nil {
		return err
	}

	password, err := x.event.GetArtifact("password")
	if err != nil {
		return err
	}

	email, err := x.event.GetArtifact("email")
	if err != nil {
		return err
	}

	dockerClient, err := docker.NewClient(x.Socket)
	err = dockerClient.PushImage(docker.PushImageOptions{
		Name:         imagePathGen(x.event),
		Tag:          imageTagGen(x.event),
		OutputStream: buildlog,
	}, docker.AuthConfiguration{
		Username: user.String(),
		Password: password.String(),
		Email:    email.String(),
	})
	if err != nil {
		return err
	}

	tagOptions := docker.TagImageOptions{
		Repo:  imagePathGen(x.event),
		Tag:   imageTagLatest(x.event),
		Force: true,
	}

	fullImagePath := imagePathGen(x.event) + ":" + imageTagGen(x.event)

	if err = dockerClient.TagImage(fullImagePath, tagOptions); err != nil {
		return err
	}

	err = dockerClient.PushImage(docker.PushImageOptions{
		Name:         imagePathGen(x.event),
		Tag:          imageTagLatest(x.event),
		OutputStream: buildlog,
	}, docker.AuthConfiguration{
		Username: user.String(),
		Password: password.String(),
		Email:    email.String(),
	})
	if err != nil {
		return err
	}

	return nil
}

func (x *DockerBuilder) Process(e transistor.Event) error {
	x.event = e
	if e.Name == "plugins.ProjectExtension:create:dockerbuilder" {
		var extensionEvent plugins.ProjectExtension
		extensionEvent = e.Payload.(plugins.ProjectExtension)
		extensionEvent.Action = plugins.GetAction("status")
		extensionEvent.State = plugins.GetState("complete")
		extensionEvent.StateMessage = "installation complete"
		ev := e.NewEvent(extensionEvent, nil)
		x.events <- ev
		return nil
	}

	if e.Name == "plugins.ProjectExtension:update:dockerbuilder" {
		var extensionEvent plugins.ProjectExtension
		extensionEvent = e.Payload.(plugins.ProjectExtension)
		extensionEvent.Action = plugins.GetAction("status")
		extensionEvent.State = plugins.GetState("complete")
		extensionEvent.StateMessage = "update complete"
		ev := e.NewEvent(extensionEvent, nil)
		x.events <- ev
		return nil
	}

	event := e.Payload.(plugins.ReleaseExtension)

	var err error

	event.Action = plugins.GetAction("status")
	event.State = plugins.GetState("fetching")
	event.StateMessage = ""
	x.events <- e.NewEvent(event, nil)

	// repoPath := fmt.Sprintf("%s/%s_%s", event.Release.Git.Workdir, event.Release.Project.Repository, event.Release.Git.Branch)
	repoPath := fmt.Sprintf("%s", event.Release.Project.Repository)

	buildlogBuf := bytes.NewBuffer(nil)
	buildlog := io.MultiWriter(buildlogBuf, os.Stdout)

	err = x.bootstrap(repoPath, event)
	if err != nil {
		log.Debug(err)
		event.State = plugins.GetState("failed")
		event.StateMessage = fmt.Sprintf("%v (Action: %v, Step: bootstrap)", err.Error(), event.State)
		x.events <- e.NewEvent(event, nil)
		return err
	}

	err = x.build(repoPath, event, buildlog)
	if err != nil {
		log.Debug(err)
		event.State = plugins.GetState("failed")
		event.StateMessage = fmt.Sprintf("%v (Action: %v, Step: build)", err.Error(), event.State)

		ev := e.NewEvent(event, nil)
		ev.AddArtifact("build_log", buildlogBuf.String(), false)
		x.events <- ev

		return err
	}

	err = x.push(repoPath, event, buildlog)
	if err != nil {
		log.Debug(err)
		event.State = plugins.GetState("failed")
		event.StateMessage = fmt.Sprintf("%v (Action: %v, Step: push)", err.Error(), event.State)

		ev := e.NewEvent(event, nil)
		ev.AddArtifact("build_log", buildlogBuf.String(), false)
		x.events <- ev

		return err
	}

	event.State = plugins.GetState("complete")
	event.StateMessage = "Completed"

	user, err := x.event.GetArtifact("user")
	if err != nil {
		return err
	}

	password, err := x.event.GetArtifact("password")
	if err != nil {
		return err
	}

	email, err := x.event.GetArtifact("email")
	if err != nil {
		return err
	}

	registryHost, err := x.event.GetArtifact("host")
	if err != nil {
		log.Error(err)
	}

	ev := e.NewEvent(event, nil)
	ev.AddArtifact("user", user.String(), user.Secret)
	ev.AddArtifact("password", password.String(), password.Secret)
	ev.AddArtifact("email", email.String(), email.Secret)
	ev.AddArtifact("host", registryHost.String(), registryHost.Secret)
	ev.AddArtifact("image", fullImagePath(x.event), false)
	ev.AddArtifact("build_log", buildlogBuf.String(), false)
	x.events <- ev

	return nil
}

// generate image tag name
func imageTagGen(event transistor.Event) string {
	return (fmt.Sprintf("%s.%s", event.Payload.(plugins.ReleaseExtension).Release.HeadFeature.Hash, event.Payload.(plugins.ReleaseExtension).Release.Environment))
}

func imageTagLatest(event transistor.Event) string {
	if event.Payload.(plugins.ReleaseExtension).Release.Environment == "production" {
		return ("latest")
	}
	return (fmt.Sprintf("%s.%s", "latest", event.Payload.(plugins.ReleaseExtension).Release.Environment))
}

// rengerate image path name
func imagePathGen(event transistor.Event) string {
	registryHost, err := event.GetArtifact("host")
	if err != nil {
		log.Error(err)
	}

	registryOrg, err := event.GetArtifact("org")
	if err != nil {
		log.Error(err)
	}

	return (fmt.Sprintf("%s/%s/%s", registryHost.String(), registryOrg.String(), slug.Slug(event.Payload.(plugins.ReleaseExtension).Release.Project.Repository)))
}

// return the full image path with featureHash tag
func fullImagePath(event transistor.Event) string {
	return (fmt.Sprintf("%s:%s", imagePathGen(event), imageTagGen(event)))
}

package mydocker

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Andrew-Morozko/orca/jobcontroller"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type Client struct {
	*client.Client
	// t string
}

func FromEnv() (myclient *Client, err error) {
	cl, err := client.NewClientWithOpts(client.FromEnv, client.WithVersion(os.Getenv("ORCA_DOCKER_VERSION")))
	if err != nil {
		return nil, err
	}
	myclient = &Client{
		Client: cl,
	}
	return
}

type Image struct {
	*types.ImageInspect
}

func wrapImage(img *types.ImageInspect) *Image {
	normLabels := make(map[string]string, len(img.Config.Labels))
	for k, v := range img.Config.Labels {
		normLabels[Normalise(k)] = v
	}
	img.Config.Labels = normLabels
	return &Image{
		ImageInspect: img,
	}
}

func (c *Client) ListLabeledImages(jc jobcontroller.JobController) ([]*Image, error) {
	images, err := c.ImageList(jc, types.ImageListOptions{
		All: false,
		Filters: filters.NewArgs(
			filters.Arg("reference", "*:latest"),
			filters.Arg("label", "orca.enabled"),
		),
	})
	if err != nil {
		return nil, err
	}

	res := make([]*Image, 0, len(images))
	for _, imgSum := range images {
		imgDetails, _, err := c.ImageInspectWithRaw(jc, imgSum.ID)
		if err != nil {
			jc.Logger.Errf(err,
				"failed to inspect image %s",
				strings.Split(imgSum.ID, ":")[1][:8],
			)
			continue
		}
		res = append(res, wrapImage(&imgDetails))
	}
	return res, nil
}

/////////////////////////////////////

func Normalise(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func (di *Image) Get(key string) (string, bool) {
	val, found := di.Config.Labels[Normalise(key)]
	if found {
		val = Normalise(val)
	}
	return val, found
}

func (di *Image) GetDefault(key string, defaultVal string) string {
	val, found := di.Get(key)
	if !found {
		val = defaultVal
	}
	return val
}

func (di *Image) GetIntDefault(key string, defaultVal int) int {
	val, found := di.Get(key)
	if found {
		res, err := strconv.Atoi(val)
		if err != nil {
			log.Printf(`["%s": "%s"] int parse error: %s`+"\n", key, val, err)
			return defaultVal
		}
		return res
	} else {
		return defaultVal
	}
}
func (di *Image) GetDurationDefault(key string, defaultVal time.Duration) time.Duration {
	val, found := di.Get(key)
	if found {
		res, err := time.ParseDuration(val)
		if err != nil {
			log.Printf(`["%s": "%s"] duration parse error: %s`+"\n", key, val, err)
			return defaultVal
		}
		return res
	} else {
		return defaultVal
	}
}

func (di *Image) GetBoolDefault(key string, defaultVal bool) bool {
	val, found := di.Get(key)
	if found {
		res, err := strconv.ParseBool(val)
		if err != nil {
			log.Printf(`["%s": "%s"] bool parse error: %s`+"\n", key, val, err)
			return defaultVal
		}
		return res
	} else {
		return defaultVal
	}
}

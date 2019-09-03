package uploadserver

import (
	"Upload/liteimp"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gin-gonic/gin"
)

func GetFileList(c *gin.Context) {
	storagepath := GetPathWhereToStore()

	var listFilter liteimp.RequestForFileList
	err := c.ShouldBindQuery(&listFilter)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad URL parameters"})
		return
	}
	isnamefilter := false
	var reg *regexp.Regexp
	if listFilter.Filter != "" {
		reg, err = regexp.Compile(listFilter.Filter)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "filter URL parameter is bad"})
			return

		}
		isnamefilter = true

	}
	type smallinf struct {
		Name string
		Size int64
	}
	listname := make([]smallinf, 0, 200)
	filepath.Walk(storagepath, func(path string, info os.FileInfo, errinfile error) error {
		if info.IsDir() && path != "." {
			return filepath.SkipDir
		}
		if isnamefilter {
			is := reg.FindString(path)
			if is == "" {
				return filepath.SkipDir
			}
		}
		listname = append(listname, smallinf{
			Name: info.Name(),
			Size: info.Size(),
		})
		return nil
	})
	c.JSON(http.StatusOK, listname)
	return
}

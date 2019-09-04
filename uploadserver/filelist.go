package uploadserver

import (
	"Upload/liteimp"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gin-gonic/gin"
)

type smallinf struct {
	Name string
	Size int64
}

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
	nameslist := fillnameslist(storagepath, isnamefilter, reg)
	tmpl, err := template.ParseFiles(filepath.Join(RunningFromDir, "htmltemplates/filelist.html"))
	if err != nil {
		log.Printf("%s", err)
		c.JSON(http.StatusOK, gin.H{"error": fmt.Errorf("can't parse html template ./htmltemplates/filelist.html : %s", err)})

		return
	}
	err = tmpl.Execute(c.Writer, nameslist)
	if err != nil {
		log.Printf("%s", err)
		c.JSON(http.StatusOK, gin.H{"error": fmt.Errorf("html template failed to execute. : %s", err)})
		return
	}

	return
}

func fillnameslist(storagepath string, isnamefilter bool, reg *regexp.Regexp) []smallinf {
	nameslist := make([]smallinf, 0, 200)
	filepath.Walk(storagepath, func(path string, info os.FileInfo, errinfile error) error {
		if info.IsDir() && path != "." {
			return filepath.SkipDir
		}
		if isnamefilter {
			is := reg.FindString(path)
			if is == "" {
				return nil // next file please
			}
		}
		nameslist = append(nameslist, smallinf{
			Name: info.Name(),
			Size: info.Size(),
		})
		return nil
	})
	return nameslist

}

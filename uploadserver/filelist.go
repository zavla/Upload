package uploadserver

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"time"

	"github.com/zavla/upload/liteimp"

	"github.com/gin-gonic/gin"
)

type smallinf struct {
	Name     string
	Size     int64
	DateTime time.Time
	Date     string
}

// GetFileList is a gin.HandlerFunc.
// Returns a response with html page "list of files"
func GetFileList(c *gin.Context) {
	username := c.Param("login")
	urlpath := c.Param("path")
	if len(urlpath) == 0 {
		urlpath += "/"
	}
	urlpathtousername := "upload/" + username

	storagepath := GetPathWhereToStoreByUsername(username)
	fullfspath := storagepath + urlpath
	// if strings.Contains(fullfspath, `..`) {
	// 	c.JSON(http.StatusBadRequest, gin.H{"error": "bad file path in URL."})
	// 	return
	// }

	var listFilter liteimp.RequestForFileList
	err := c.ShouldBindQuery(&listFilter)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expecting '?filter=*' URL parameter"})
		return
	}
	isnamefilter := false
	var reg *regexp.Regexp
	if listFilter.Filter != "" {
		reg, err = regexp.Compile(listFilter.Filter)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "'filter' URL parameter regexp is bad"})
			return

		}
		isnamefilter = true

	}
	stat, err := os.Stat(fullfspath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, gin.H{"error": "no files yet"})
		} else {
			log.Printf("For user %s, error while reading his directory: %s\r\n", username, err)
			c.JSON(http.StatusForbidden, gin.H{"error": "Unexpected directory structure"})
		}
		return

	}
	if !stat.IsDir() {
		log.Printf("For user %s, error while reading his directory: not a directory: %s\r\n", username, fullfspath)
		c.JSON(http.StatusForbidden, gin.H{"error": "Unexpected directory structure"})
		return
	}

	nameslist := fillnameslist(fullfspath, isnamefilter, reg)
	tmpl, err := template.ParseFiles(filepath.Join(ConfigThisService.RunningFromDir, "htmltemplates/filelist.html"))
	if err != nil {
		log.Printf("%s\n", err)
		c.JSON(http.StatusOK, gin.H{"error": fmt.Errorf("can't parse html template ./htmltemplates/filelist.html : %s", err)})

		return
	}
	type topage struct {
		Path   string
		Files  []smallinf
		Parent string
	}
	parent := path.Dir(urlpath)
	vtopage := topage{
		Path:   urlpathtousername + urlpath,
		Files:  nameslist,
		Parent: urlpathtousername + parent,
	}
	err = tmpl.Execute(c.Writer, vtopage)
	if err != nil {
		log.Printf("%s\n", err)
		c.JSON(http.StatusOK, gin.H{"error": fmt.Errorf("html template failed to execute. : %s", err)})
		return
	}

}

func fillnameslist(storagepath string, isnamefilter bool, reg *regexp.Regexp) []smallinf {
	nameslist := make([]smallinf, 0, 200)
	_ = filepath.Walk(storagepath, func(path string, info os.FileInfo, errinfile error) error {
		if errinfile != nil {
			return filepath.SkipDir
		}
		if info.IsDir() && path == storagepath {
			return nil //next file
		}
		if info.IsDir() {
			nameslist = append(nameslist, smallinf{
				Name:     info.Name(),
				Size:     info.Size(),
				DateTime: info.ModTime(),
				Date:     info.ModTime().Format(http.TimeFormat),
			})
			return filepath.SkipDir
		}
		if isnamefilter {
			is := reg.FindString(path)
			if is == "" {
				return nil // next file please
			}
		}
		nameslist = append(nameslist, smallinf{
			Name:     info.Name(),
			Size:     info.Size(),
			DateTime: info.ModTime(),
			Date:     info.ModTime().Format(http.TimeFormat),
		})
		return nil
	})
	return nameslist

}

package download

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// 线程数
var Processer int = runtime.NumCPU() / 2

/*
interface不是struct， 不能直接输出struct的属性，
所以需要释放一些获取属性的方法,提供使用
*/
type Downloader interface {
	Download() error     // 开始下载
	GetPath() string     // 获取下载到的目录
	GetFileName() string // 获取目标文件名
}

/*
分片
*/
type shard struct {
	// Url      string
	FileName string
	Index    int
	From     int64
	To       int64
	Size     int64
}

func (s *shard) down(url string, wg *sync.WaitGroup) error {
	defer wg.Done()
	/*
		\	如果文件存在，则断点续传
		/	如果不存在就正常下载
	*/
	var isExist bool
	fileInfo, err := os.Stat(s.FileName)
	// 文件存在
	// 文件如果没有下载完
	if err == nil {
		isExist = true
		size := fileInfo.Size()
		if size < s.Size {
			s.From += size
		}
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", s.From, s.To))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode > 299 {
		return fmt.Errorf("response code error")
	}
	// fmt.Println(t.FileName)
	var f *os.File
	if isExist {
		f, err = os.OpenFile(s.FileName, os.O_RDWR, os.ModePerm)
		if err != nil {
			return err
		}
		_, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return err
		}
	} else {
		f, err = os.Create(s.FileName)
		if err != nil {
			return err
		}
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)

	return err
}

type packageInfo struct {
	Url      string
	FileName string
	Dir      string
	Length   int64
	WG       *sync.WaitGroup
	Threads  int

	Shards []*shard
}

/*
创建一个下载器,用于下载.

<url>: 目标的URL;

[dir]: 存放的目录.
*/
func New(url string, dir ...string) *packageInfo {
	p := packageInfo{
		Url:      url,
		FileName: path.Base(url),
		Threads:  Processer,
		WG:       new(sync.WaitGroup),
	}
	var destDir string
	if len(dir) == 0 {
		destDir = ""
	} else {
		destDir = dir[0]
	}
	p.SetDir(destDir)
	return &p
}
func (p *packageInfo) GetPath() string {
	return p.Dir + "/" + p.FileName
}

func (p *packageInfo) GetFileName() string {
	return p.FileName
}
func (p *packageInfo) SetThread(t int) {
	if t == 0 {
		t = Processer
	}
	p.Threads = t
}
func (p *packageInfo) SetDir(dir string) {
	// 空目录
	if dir == "" {
		dir, _ = os.Getwd()
		goto RETURN
	}
	// 去掉后边的"/"
	if strings.HasSuffix(dir, "/") {
		dir = strings.TrimRight(dir, "/")
	}
	// 不是绝对路径
	if !filepath.IsAbs(dir) {
		// 相对路径
		if strings.HasPrefix(dir, "./") || strings.HasPrefix(dir, "../") {
			dir, _ = filepath.Abs(dir)
		} else {
			// 只有目录名
			wd, _ := os.Getwd()
			dir = wd + "/" + dir
		}
	}
RETURN:
	p.Dir = dir
}
func (p *packageInfo) Download() error {
	accept, err := p.AcceptRages()
	if err != nil {
		return err
	}
	if accept {
		p.multiTreadDown()
	} else {
		p.sigleDown()
	}
	return nil
}

/*
是否支持分段断点续传，
如果支持断点则获取长度
*/
func (p *packageInfo) AcceptRages() (accept bool, err error) {
	req, err := http.NewRequest(http.MethodHead, p.Url, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("response code: %d", resp.StatusCode)
		return
	}
	defer resp.Body.Close()
	if resp.Header.Get("Accept-Ranges") != "bytes" {
		return
	}
	accept = true
	length, err := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	if err != nil {
		return
	}
	p.Length = length
	return
}

func (p *packageInfo) multiTreadDown() error {
	_, err := os.Stat(p.Dir)
	if os.IsNotExist(err) {
		err = os.MkdirAll(p.Dir, os.ModeDir|os.ModePerm)
	}
	if err != nil {
		return err
	}
	size := int64(math.Ceil(float64(p.Length) / float64(p.Threads)))
	var start, end int64
	for i := 0; i < p.Threads; i++ {
		if i == p.Threads-1 {
			end = p.Length - 1
		} else {
			end = start + size - 1
		}
		shard := &shard{
			FileName: fmt.Sprintf("%s/.%s.%d", p.Dir, "temp", i),
			Index:    i,
			From:     start,
			To:       end,
			// Url:      p.Url,
			Size: end - start + 1,
		}
		p.Shards = append(p.Shards, shard)
		start += size
	}

	for _, shard := range p.Shards {
		p.WG.Add(1)
		go shard.down(p.Url, p.WG)
	}

	p.WG.Wait()
	if err := p.merge(); err != nil {
		return err
	}
	return nil
}

// 合并分片成一个文件
func (p *packageInfo) merge() error {
	fp := p.Dir + "/" + p.FileName
	dest, err := os.OpenFile(fp, os.O_CREATE|os.O_WRONLY, os.ModePerm)
	if err != nil {
		return err
	}
	for _, v := range p.Shards {
		f, err := os.Open(v.FileName)
		if err != nil {
			return err
		}
		defer f.Close()
		size, err := io.Copy(dest, f)
		if err != nil {
			return err
		}
		if size != int64(v.Size) {
			return fmt.Errorf("file size failure")
		}
		defer os.Remove(v.FileName)
	}
	return nil
}

func (p *packageInfo) sigleDown() error {
	resp, err := http.Get(p.Url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(p.FileName)
	if err != nil {
		return err
	}
	r := bufio.NewReader(resp.Body)
	w := bufio.NewWriter(f)
	_, err = io.Copy(w, r)
	if err != nil {
		return err
	}
	return nil
}

func Md5sum(fp string) (md5sum string, err error) {
	f, err := os.Open(fp)
	if err != nil {
		return
	}
	reader := bufio.NewReader(f)
	h := md5.New()
	_, err = io.Copy(h, reader)
	if err != nil {
		return
	}

	md5sum = fmt.Sprintf("%x", h.Sum(nil))
	return
}

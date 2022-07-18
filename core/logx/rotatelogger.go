package logx

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zeromicro/go-zero/core/fs"
	"github.com/zeromicro/go-zero/core/lang"
)

const (
	rfc3339DateFormat = time.RFC3339
	dateFormat        = "2006-01-02"
	hoursPerDay       = 24
	bufferSize        = 100
	defaultDirMode    = 0o755
	defaultFileMode   = 0o600
	gzipExt           = ".gz"
	megabyte          = 1024 * 1024
)

// ErrLogFileClosed is an error that indicates the log file is already closed.
var ErrLogFileClosed = errors.New("error: log file closed")

type (
	// A RotateRule interface is used to define the log rotating rules.
	RotateRule interface {
		BackupFileName() string
		MarkRotated()
		OutdatedFiles() []string
		ShallRotate(currentSize, writeLen int) bool
	}

	// A RotateLogger is a Logger that can rotate log files with given rules.
	RotateLogger struct {
		filename string
		backup   string
		fp       *os.File
		channel  chan []byte
		done     chan lang.PlaceholderType
		rule     RotateRule
		compress bool
		// can't use threading.RoutineGroup because of cycle import
		waitGroup sync.WaitGroup
		closeOnce sync.Once

		currentSize int
	}

	// A DailyRotateRule is a rule to daily rotate the log files.
	DailyRotateRule struct {
		rotatedTime string
		filename    string
		delimiter   string
		days        int
		gzip        bool
	}

	// SizeLimitRotateRule a rotation rule that make the log file rotated base on size
	SizeLimitRotateRule struct {
		DailyRotateRule
		maxSize    int
		maxBackups int
	}
)

// DefaultRotateRule is a default log rotating rule, currently DailyRotateRule.
func DefaultRotateRule(filename, delimiter string, days int, gzip bool) RotateRule {
	return &DailyRotateRule{
		rotatedTime: getNowDate(),
		filename:    filename,
		delimiter:   delimiter,
		days:        days,
		gzip:        gzip,
	}
}

// BackupFileName returns the backup filename on rotating.
func (r *DailyRotateRule) BackupFileName() string {
	return fmt.Sprintf("%s%s%s", r.filename, r.delimiter, getNowDate())
}

// MarkRotated marks the rotated time of r to be the current time.
func (r *DailyRotateRule) MarkRotated() {
	r.rotatedTime = getNowDate()
}

// OutdatedFiles returns the files that exceeded the keeping days.
func (r *DailyRotateRule) OutdatedFiles() []string {
	if r.days <= 0 {
		return nil
	}

	var pattern string
	if r.gzip {
		pattern = fmt.Sprintf("%s%s*%s", r.filename, r.delimiter, gzipExt)
	} else {
		pattern = fmt.Sprintf("%s%s*", r.filename, r.delimiter)
	}

	files, err := filepath.Glob(pattern)
	if err != nil {
		Errorf("failed to delete outdated log files, error: %s", err)
		return nil
	}

	var buf strings.Builder
	boundary := time.Now().Add(-time.Hour * time.Duration(hoursPerDay*r.days)).Format(dateFormat)
	fmt.Fprintf(&buf, "%s%s%s", r.filename, r.delimiter, boundary)
	if r.gzip {
		buf.WriteString(gzipExt)
	}
	boundaryFile := buf.String()

	var outdates []string
	for _, file := range files {
		if file < boundaryFile {
			outdates = append(outdates, file)
		}
	}

	return outdates
}

// ShallRotate checks if the file should be rotated.
func (r *DailyRotateRule) ShallRotate(currentSize, writeLen int) bool {
	return len(r.rotatedTime) > 0 && getNowDate() != r.rotatedTime
}

// NewSizeLimitRotateRule returns the rotation rule with size limit
func NewSizeLimitRotateRule(filename, delimiter string, days, maxSize, maxBackups int, gzip bool) RotateRule {
	return &SizeLimitRotateRule{
		DailyRotateRule: DailyRotateRule{
			rotatedTime: getNowDateInRFC3339Format(),
			filename:    filename,
			delimiter:   delimiter,
			days:        days,
			gzip:        gzip,
		},
		maxSize:    maxSize,
		maxBackups: maxBackups,
	}
}

func (r *SizeLimitRotateRule) ShallRotate(currentSize, writeLen int) bool {
	return r.maxSize > 0 && r.maxSize*megabyte < currentSize+writeLen
}

func (r *SizeLimitRotateRule) parseFilename(file string) (dir, logname, ext, prefix string) {
	dir = filepath.Dir(r.filename)
	logname = filepath.Base(r.filename)
	ext = filepath.Ext(r.filename)
	prefix = logname[:len(logname)-len(ext)]
	return
}

func (r *SizeLimitRotateRule) BackupFileName() string {
	dir := filepath.Dir(r.filename)
	_, _, ext, prefix := r.parseFilename(r.filename)
	timestamp := getNowDateInRFC3339Format()
	return filepath.Join(dir, fmt.Sprintf("%s%s%s%s", prefix, r.delimiter, timestamp, ext))
}

func (r *SizeLimitRotateRule) MarkRotated() {
	r.rotatedTime = getNowDateInRFC3339Format()
}

func (r *SizeLimitRotateRule) OutdatedFiles() []string {
	var pattern string
	dir, _, ext, prefix := r.parseFilename(r.filename)
	if r.gzip {
		pattern = fmt.Sprintf("%s%s%s%s*%s%s", dir, string(filepath.Separator), prefix, r.delimiter, ext, gzipExt)
	} else {
		pattern = fmt.Sprintf("%s%s%s%s*%s", dir, string(filepath.Separator), prefix, r.delimiter, ext)
	}

	files, err := filepath.Glob(pattern)
	if err != nil {
		fmt.Printf("failed to delete outdated log files, error: %s\n", err)
		Errorf("failed to delete outdated log files, error: %s", err)
		return nil
	}

	sort.Strings(files)

	outdated := make(map[string]lang.PlaceholderType)

	// test if too many backups
	if r.maxBackups > 0 && len(files) > r.maxBackups {
		for _, f := range files[:len(files)-r.maxBackups] {
			outdated[f] = lang.Placeholder
		}
		files = files[len(files)-r.maxBackups:]
	}

	// test if any too old backups
	if r.days > 0 {
		boundary := time.Now().Add(-time.Hour * time.Duration(hoursPerDay*r.days)).Format(rfc3339DateFormat)
		bf := filepath.Join(dir, fmt.Sprintf("%s%s%s%s", prefix, r.delimiter, boundary, ext))
		if r.gzip {
			bf += gzipExt
		}
		for _, f := range files {
			if f < bf {
				outdated[f] = lang.Placeholder
			} else {
				// Becase the filenames are sorted. No need to keep looping after the first ineligible item showing up.
				break
			}
		}
	}

	var result []string
	for k := range outdated {
		result = append(result, k)
	}
	return result
}

func (r *SizeLimitRotateRule) parseBackupTime(file string) (time.Time, error) {
	if r.gzip {
		file = file[:len(file)-len(gzipExt)]
	}
	file = file[:len(file)-len(filepath.Ext(file))]
	s := strings.Split(file, r.delimiter)
	var t string
	if len(s) != 2 {
		err := fmt.Errorf("Invalid backup log filename: %s", file)
		Error(err)
		return time.Time{}, err
	}
	tt, err := time.Parse(rfc3339DateFormat, t)
	if err != nil {
		Errorf("Failed to parse backup time from backup log file: %s", file)
		return time.Time{}, err
	}
	return tt, nil
}

// NewLogger returns a RotateLogger with given filename and rule, etc.
func NewLogger(filename string, rule RotateRule, compress bool) (*RotateLogger, error) {
	l := &RotateLogger{
		filename: filename,
		channel:  make(chan []byte, bufferSize),
		done:     make(chan lang.PlaceholderType),
		rule:     rule,
		compress: compress,
	}
	if err := l.init(); err != nil {
		return nil, err
	}

	l.startWorker()
	return l, nil
}

// Close closes l.
func (l *RotateLogger) Close() error {
	var err error

	l.closeOnce.Do(func() {
		close(l.done)
		l.waitGroup.Wait()

		if err = l.fp.Sync(); err != nil {
			return
		}

		err = l.fp.Close()
	})

	return err
}

func (l *RotateLogger) Write(data []byte) (int, error) {
	select {
	case l.channel <- data:
		return len(data), nil
	case <-l.done:
		log.Println(string(data))
		return 0, ErrLogFileClosed
	}
}

func (l *RotateLogger) getBackupFilename() string {
	if len(l.backup) == 0 {
		return l.rule.BackupFileName()
	}

	return l.backup
}

func (l *RotateLogger) init() error {
	l.backup = l.rule.BackupFileName()

	if _, err := os.Stat(l.filename); err != nil {
		basePath := path.Dir(l.filename)
		if _, err = os.Stat(basePath); err != nil {
			if err = os.MkdirAll(basePath, defaultDirMode); err != nil {
				return err
			}
		}

		if l.fp, err = os.Create(l.filename); err != nil {
			return err
		}
	} else if l.fp, err = os.OpenFile(l.filename, os.O_APPEND|os.O_WRONLY, defaultFileMode); err != nil {
		return err
	}

	fs.CloseOnExec(l.fp)

	return nil
}

func (l *RotateLogger) maybeCompressFile(file string) {
	if !l.compress {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			ErrorStack(r)
		}
	}()

	if _, err := os.Stat(file); err != nil {
		// file not exists or other error, ignore compression
		return
	}

	compressLogFile(file)
}

func (l *RotateLogger) maybeDeleteOutdatedFiles() {
	files := l.rule.OutdatedFiles()
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			Errorf("failed to remove outdated file: %s", file)
		}
	}
}

func (l *RotateLogger) postRotate(file string) {
	go func() {
		// we cannot use threading.GoSafe here, because of import cycle.
		l.maybeCompressFile(file)
		l.maybeDeleteOutdatedFiles()
	}()
}

func (l *RotateLogger) rotate() error {
	if l.fp != nil {
		err := l.fp.Close()
		l.fp = nil
		if err != nil {
			return err
		}
	}

	_, err := os.Stat(l.filename)
	if err == nil && len(l.backup) > 0 {
		backupFilename := l.getBackupFilename()
		err = os.Rename(l.filename, backupFilename)
		if err != nil {
			return err
		}

		l.postRotate(backupFilename)
	}

	l.backup = l.rule.BackupFileName()
	if l.fp, err = os.Create(l.filename); err == nil {
		fs.CloseOnExec(l.fp)
	}

	return err
}

func (l *RotateLogger) startWorker() {
	l.waitGroup.Add(1)

	go func() {
		defer l.waitGroup.Done()

		for {
			select {
			case event := <-l.channel:
				l.write(event)
			case <-l.done:
				return
			}
		}
	}()
}

func (l *RotateLogger) write(v []byte) {
	if l.rule.ShallRotate(l.currentSize, len(v)) {
		if err := l.rotate(); err != nil {
			log.Println(err)
		} else {
			l.rule.MarkRotated()
			l.currentSize = 0
		}
	}
	if l.fp != nil {
		l.fp.Write(v)
		l.currentSize += len(v)
	}
}

func compressLogFile(file string) {
	start := time.Now()
	Infof("compressing log file: %s", file)
	if err := gzipFile(file); err != nil {
		Errorf("compress error: %s", err)
	} else {
		Infof("compressed log file: %s, took %s", file, time.Since(start))
	}
}

func getNowDate() string {
	return time.Now().Format(dateFormat)
}

func getNowDateInRFC3339Format() string {
	return time.Now().Add((-24*60 + 5) * time.Minute).Format(rfc3339DateFormat)
}

func gzipFile(file string) error {
	in, err := os.Open(file)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(fmt.Sprintf("%s%s", file, gzipExt))
	if err != nil {
		return err
	}
	defer out.Close()

	w := gzip.NewWriter(out)
	if _, err = io.Copy(w, in); err != nil {
		return err
	} else if err = w.Close(); err != nil {
		return err
	}

	return os.Remove(file)
}

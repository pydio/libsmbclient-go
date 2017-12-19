package libsmbclient

import (
	"io"
	"sync"
	"unsafe"
	"syscall"
	"errors"
)

/*
#cgo LDFLAGS: -lsmbclient 
#cgo CFLAGS: -I/usr/include/samba-4.0
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include <libsmbclient.h>

SMBCFILE* my_smbc_opendir(SMBCCTX *c, const char *fname);
int my_smbc_closedir(SMBCCTX *c, SMBCFILE *dir);
struct smbc_dirent* my_smbc_readdir(SMBCCTX *c, SMBCFILE *dir);
SMBCFILE* my_smbc_open(SMBCCTX *c, const char *fname, int flags, mode_t mode);
ssize_t my_smbc_read(SMBCCTX *c, SMBCFILE *file, void *buf, size_t count);
void my_smbc_close(SMBCCTX *c, SMBCFILE *f);
void my_smbc_auth_callback(SMBCCTX *context,
               const char *server_name, const char *share_name,
	       char *domain_out, int domainmaxlen,
	       char *username_out, int unmaxlen,
	       char *password_out, int pwmaxlen);
void my_smbc_init_auth_callback(SMBCCTX *context, void *go_fn);
off_t my_smbc_lseek(SMBCCTX *c, SMBCFILE * file, off_t offset, int whence);
int my_smbc_stat(SMBCCTX *c, const char *url, struct stat *st);

typedef struct stat c_stat;
*/
import "C" // DO NOT CHANGE THE POSITION OF THIS IMPORT

// SmbType
type SmbcType int

const (
	SMBC_WORKGROUP     SmbcType = C.SMBC_WORKGROUP
	SMBC_FILE_SHARE             = C.SMBC_FILE_SHARE
	SMBC_PRINTER_SHARE          = C.SMBC_PRINTER_SHARE
	SMBC_COMMS_SHARE            = C.SMBC_COMMS_SHARE
	SMBC_IPC_SHARE              = C.SMBC_IPC_SHARE
	SMBC_DIR                    = C.SMBC_DIR
	SMBC_FILE                   = C.SMBC_FILE
	SMBC_LINK                   = C.SMBC_LINK
)

type Dirent struct {
	Type    SmbcType
	Comment string
	Name    string
}

type ino_t int
type mode_t int
type nlink_t int
type uid_t int
type gid_t int
type dev_t int32
type off_t int32
type blksize_t int32
type blkcnt_t uint32
type time_t int64


type Stat struct {
	/*struct stat {
		dev_t     st_dev;      ID du périphérique contenant le fichier
		ino_t     st_ino;      Numéro inœud
		mode_t    st_mode;     Protection
		nlink_t   st_nlink;    Nb liens matériels
		uid_t     st_uid;      UID propriétaire
		gid_t     st_gid;      GID propriétaire
		dev_t     st_rdev;     ID périphérique (si fichier spécial)
		off_t     st_size;     Taille totale en octets
		blksize_t st_blksize;  Taille de bloc pour E/S
		blkcnt_t  st_blocks;   Nombre de blocs alloués
		time_t    st_atime;    Heure dernier accès
		time_t    st_mtime;    Heure dernière modification
		time_t    st_ctime;    Heure dernier changement état
	};*/
	Dev        dev_t
	INode      ino_t
	Mode       mode_t
	NLink      nlink_t
	Uid        uid_t
	Gid        gid_t
	RDev       dev_t
	Size       off_t
	BlockSize  blksize_t
	BlockCount blkcnt_t
	Access     time_t
	Edit       time_t
	State      time_t
}


// *sigh* even with libsmbclient-4.0 the library is not MT safe, 
// e.g. smbc_init_context from multiple threads crashes
var global_lock *sync.Mutex = &sync.Mutex{}

// client interface
type Client struct {
	ctx          *C.SMBCCTX
	authCallback *func(string, string) (string, string, string)
	// libsmbclient is not thread safe
	lock *sync.Mutex
}

// File wrapper
type File struct {
	client   *Client
	smbcfile *C.SMBCFILE
}

type FileContent struct {
	client *Client
	smbcFile *C.SMBFILE
	offset int64
	totalSize int64
	closed bool
}

func (fc *FileContent) Read(b []byte) (int, error) {
	if fc.closed {
		return -1, io.ErrClosedPipe
	}

	if fc.offset == fc.totalSize {
		return 0, io.EOF
	}

	fc.client.lock.Lock()
	defer fc.client.lock.Unlock()

	c_count, err := C.my_smbc_read(fc.client.ctx, fc.smbcFile, unsafe.Pointer(&b[0]), C.size_t(len(b)))
	if c_count == 0 && err == nil {
		return 0, io.EOF
	}
	fc.offset += int64(c_count)
	return int(c_count), err
}

func (fc *FileContent) Close() error {
	fc.closed = true
	return nil
}


// authentication callback, this expects a go callback function
// with the signature:
//  func(server_name, share_name) (domain, username, password)
func (c *Client) SetAuthCallback(fn func(string, string) (string, string, string)) {
	c.lock.Lock()
	defer c.lock.Unlock()

	C.my_smbc_init_auth_callback(c.ctx, unsafe.Pointer(&fn))
	// we need to store it in the Client struct to ensure its not garbage
	// collected later (I think)
	c.authCallback = &fn
}

func New() *Client {
	global_lock.Lock()
	defer global_lock.Unlock()

	c := &Client{ctx: C.smbc_new_context(),
		lock: global_lock}
	C.smbc_init_context(c.ctx)
	// this does not work reliable, see TestLibsmbclientThreaded test
	/*
		runtime.SetFinalizer(c, func(c2 *Client) {
			fmt.Println(fmt.Sprintf("d: %v", c2))
			c2.Close()
		})
	*/

	return c
}

func (c *Client) Destroy() error {
	return c.Close()
}

func (c *Client) Close() error {
	// FIXME: is there a more elegant way for this c.lock.Lock() that
	//        needs to be part of every function? python decorator to
	//        the rescue :)
	c.lock.Lock()
	defer c.lock.Unlock()

	var err error
	if c.ctx != nil {
		// 1 would mean we force the destroy
		_, err = C.smbc_free_context(c.ctx, C.int(1))
		c.ctx = nil
	}
	return err
}

// options
func (c *Client) GetDebug() int {
	c.lock.Lock()
	defer c.lock.Unlock()

	return int(C.smbc_getDebug(c.ctx))
}

func (c *Client) SetDebug(level int) {
	c.lock.Lock()
	defer c.lock.Unlock()

	C.smbc_setDebug(c.ctx, C.int(level))
}

func (c *Client) GetUser() string {
	c.lock.Lock()
	defer c.lock.Unlock()

	return C.GoString(C.smbc_getUser(c.ctx))
}

func (c *Client) SetUser(user string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	C.smbc_setUser(c.ctx, C.CString(user))
}

func (c *Client) GetWorkgroup() string {
	c.lock.Lock()
	defer c.lock.Unlock()

	return C.GoString(C.smbc_getWorkgroup(c.ctx))
}

func (c *Client) SetWorkgroup(wg string) {
	c.lock.Lock()
	defer c.lock.Unlock()

	C.smbc_setWorkgroup(c.ctx, C.CString(wg))
}

func (c *Client) Opendir(durl string) (File, error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	d, err := C.my_smbc_opendir(c.ctx, C.CString(durl))
	return File{client: c, smbcfile: d}, err
}

func (c *Client) Stat(url string) (*Stat, error) {
	var stat C.c_stat
	r := C.my_smbc_stat(c.ctx, C.CString(url), &stat)
	switch int(r) {
	case 0:
		//success
		return &Stat{
			Dev:        dev_t(stat.st_dev),
			INode:      ino_t(stat.st_ino),
			Mode:       mode_t(stat.st_mode),
			NLink:      nlink_t(stat.st_nlink),
			Uid:        uid_t(stat.st_uid),
			Gid:        gid_t(stat.st_gid),
			RDev:       dev_t(stat.st_rdev),
			Size:       off_t(stat.st_size),
			BlockSize:  blksize_t(stat.st_blksize),
			BlockCount: blkcnt_t(stat.st_blocks),
			Access:     time_t(stat.st_atim.tv_sec),
			Edit:       time_t(stat.st_mtim.tv_sec),
			State:      time_t(stat.st_ctim.tv_sec),
		}, nil

	case int(syscall.ENOENT):
		return nil, errors.New("a component of the path file_name does not exists")

	case int(syscall.EINVAL):
		return nil, errors.New("a NULL url was passed or smbc_init not called")

	case int(syscall.EACCES):
		return nil, errors.New("permission denied")

	case int(syscall.ENOMEM):
		return nil, errors.New("out of memory")

	case int(syscall.ENOTDIR):
		return nil, errors.New("the target dir, url, is not a directory")
	}
	return nil, nil
}

func (c *Client) GetFileWriter(url string) (io.WriteCloser, error) {
	return nil, nil
}


// dir stuff

func (dir *File) Closedir() error {
	dir.client.lock.Lock()
	defer dir.client.lock.Unlock()

	if dir.smbcfile != nil {
		_, err := C.my_smbc_closedir(dir.client.ctx, dir.smbcfile)
		dir.smbcfile = nil
		return err
	}
	return nil
}

func (dir *File) Readdir() (*Dirent, error) {
	dir.client.lock.Lock()
	defer dir.client.lock.Unlock()

	c_dirent, err := C.my_smbc_readdir(dir.client.ctx, dir.smbcfile)
	if err != nil {
		return nil, err
	}
	if c_dirent == nil {
		return nil, io.EOF
	}
	dirent := Dirent{Type: SmbcType(c_dirent.smbc_type),
		Comment: C.GoStringN(c_dirent.comment, C.int(c_dirent.commentlen)),
		Name: C.GoStringN(&c_dirent.name[0], C.int(c_dirent.namelen))}
	return &dirent, err
}

// file stuff

// FIXME: mode is actually "mode_t mode"
func (c *Client) Open(furl string, flags int, mode int) (File, error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	cs := C.CString(furl)
	sf, err := C.my_smbc_open(c.ctx, cs, C.int(flags), C.mode_t(mode))
	return File{client: c, smbcfile: sf}, err
}

func (f *File) Read(buf []byte) (int, error) {
	f.client.lock.Lock()
	defer f.client.lock.Unlock()

	c_count, err := C.my_smbc_read(f.client.ctx, f.smbcfile, unsafe.Pointer(&buf[0]), C.size_t(len(buf)))
	if c_count == 0 && err == nil {
		return 0, io.EOF
	}
	return int(c_count), err
}

func (f *File) Lseek(offset, whence int) (int, error) {
	f.client.lock.Lock()
	defer f.client.lock.Unlock()

	new_offset, err := C.my_smbc_lseek(f.client.ctx, f.smbcfile, C.off_t(offset), C.int(whence))
	return int(new_offset), err
}

func (f *File) Close() {
	f.client.lock.Lock()
	defer f.client.lock.Unlock()

	if f.smbcfile != nil {
		C.my_smbc_close(f.client.ctx, f.smbcfile)
		f.smbcfile = nil
	}
}

// INTERNAL use only
//export GoAuthCallbackHelper
func GoAuthCallbackHelper(fn unsafe.Pointer, server_name, share_name *C.char, domain_out *C.char, domain_len C.int, username_out *C.char, ulen C.int, password_out *C.char, pwlen C.int) {
	go_fn := *(*func(server_name, share_name string) (string, string, string))(fn)
	domain, user, pw := go_fn(C.GoString(server_name), C.GoString(share_name))
	C.strncpy(domain_out, C.CString(domain), C.size_t(domain_len))
	C.strncpy(username_out, C.CString(user), C.size_t(ulen))
	C.strncpy(password_out, C.CString(pw), C.size_t(pwlen))
}

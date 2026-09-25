package hy2

import (
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// certLoader 给 hy2 提供带缓存、支持热更新的证书。
//
// 以前 GetCertificate 在每次握手时都 tls.LoadX509KeyPair 重新读、重新解析
// 证书和私钥文件，有三个问题：
//   - 慢：每次握手多花 ~36µs（EC P-256）到 ~150µs（RSA-2048），外加上百次内存分配；
//   - 证书轮换写到一半时进来的握手会读到残缺文件，直接握手失败；
//   - 闭包里读的是控制器的 *CertConfig，而面板热重建时控制器会改写它，数据竞争。
//
// 现在每次握手只 stat 两个文件（~2µs），修改时间没变就直接用缓存；
// 文件暂时读不到或新文件加载失败时，继续用上一张能用的证书，不让握手失败。
// 路径在构建时按值拷贝，之后与控制器的配置再无关系。
//
// 实现移植自 hysteria 官方的 LocalCertificateLoader
// （github.com/apernet/hysteria app/internal/utils/certloader.go，MIT 协议），
// 那是 app 模块的 internal 包，没法直接引用。
type certLoader struct {
	certFile string
	keyFile  string

	lock  sync.Mutex
	cache atomic.Pointer[certCache]

	// onReload 在从磁盘重新加载后调用（成功时 err 为 nil），用于打日志。可为 nil。
	onReload func(err error)
}

// certCache 构造后只读；更新时整体替换。
type certCache struct {
	cert        *tls.Certificate
	certModTime time.Time
	keyModTime  time.Time
}

func newCertLoader(certFile, keyFile string) *certLoader {
	return &certLoader{certFile: certFile, keyFile: keyFile}
}

// init 首次加载。启动时证书就不可用应当直接报错，与原来的行为一致。
func (l *certLoader) init() error {
	l.lock.Lock()
	defer l.lock.Unlock()
	c, err := l.load()
	if err != nil {
		return err
	}
	l.cache.Store(c)
	return nil
}

func (l *certLoader) modTimes() (certMod, keyMod time.Time, err error) {
	fi, err := os.Stat(l.certFile)
	if err != nil {
		return certMod, keyMod, fmt.Errorf("stat cert file: %w", err)
	}
	certMod = fi.ModTime()
	fi, err = os.Stat(l.keyFile)
	if err != nil {
		return certMod, keyMod, fmt.Errorf("stat key file: %w", err)
	}
	return certMod, fi.ModTime(), nil
}

// load 先取修改时间再读文件：读完之后文件又变了的话，下次握手时间对不上会再加载一次。
func (l *certLoader) load() (*certCache, error) {
	certMod, keyMod, err := l.modTimes()
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(l.certFile, l.keyFile)
	if err != nil {
		return nil, err
	}
	return &certCache{cert: &cert, certModTime: certMod, keyModTime: keyMod}, nil
}

func (l *certLoader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cache := l.cache.Load()

	certMod, keyMod, err := l.modTimes()
	if err != nil {
		if cache != nil {
			return cache.cert, nil // 文件暂时不可用（比如正在替换），继续用缓存
		}
		return nil, err
	}
	if cache != nil && cache.certModTime.Equal(certMod) && cache.keyModTime.Equal(keyMod) {
		return cache.cert, nil
	}

	if cache != nil {
		if !l.lock.TryLock() {
			return cache.cert, nil // 别的握手正在重新加载，不必排队等它
		}
	} else {
		l.lock.Lock()
	}
	defer l.lock.Unlock()

	if cur := l.cache.Load(); cur != cache {
		return cur.cert, nil // 拿到锁之前已经有人加载好了
	}
	fresh, err := l.load()
	if l.onReload != nil {
		l.onReload(err)
	}
	if err != nil {
		if cache != nil {
			// 新文件还不完整（比如证书换了、私钥还没换），继续用旧证书；
			// 缓存没更新，下次握手会再试。
			return cache.cert, nil
		}
		return nil, err
	}
	l.cache.Store(fresh)
	return fresh.cert, nil
}

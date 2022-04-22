package des

import (
	"encoding/json"
	"reflect"
	"sync"
	"unsafe"

	"github.com/marspere/goencrypt"
)

var (
	des  *Des
	once sync.Once
)

type Des struct {
	cipher *goencrypt.CipherDES
}

//engine
func Engine(secretKey, iv []byte) *Des {
	once.Do(func() {
		des = new(Des)
		des.cipher = goencrypt.NewDESCipher(secretKey, iv, goencrypt.ECBMode, goencrypt.Pkcs7, goencrypt.PrintBase64)
	})
	return des
}

//DES加密
//iv为空则采用ECB模式，否则采用CBC模式
func (des *Des) Encrypt(value interface{}) (string, error) {
	buff, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return des.cipher.DESEncrypt(buff)
}

//DES解密
//iv为空则采用ECB模式，否则采用CBC模式
func (des *Des) Decrypt(cipherText string, value interface{}) error {
	plainText, err := des.cipher.DESDecrypt(cipherText)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(String2Bytes(plainText), value); err != nil {
		return err
	}
	return nil
}

func String2Bytes(s string) []byte {
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	bh := reflect.SliceHeader{
		Data: sh.Data,
		Len:  sh.Len,
		Cap:  sh.Len,
	}
	return *(*[]byte)(unsafe.Pointer(&bh))
}

package handlers

import (
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"simnexus-go/config"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// captchaChars 排除了易混淆字符 0/O/1/I/l，降低用户辨认难度。
const captchaChars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

var captchaPalettes = [][3]int{
	{220, 38, 38}, {37, 130, 211}, {22, 163, 74}, {124, 58, 237},
	{234, 88, 12}, {15, 118, 110}, {190, 18, 60}, {79, 70, 229},
}

// GetCaptcha godoc
// @Summary 获取图形验证码
// @Description 返回 SVG 验证码图片和 JWT 签名答案 token
// @Tags 认证
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/v1/auth/captcha [get]
func GetCaptcha(c *gin.Context) {
	code := make([]byte, 4)
	for i := range code {
		code[i] = captchaChars[rand.Intn(len(captchaChars))]
	}
	claims := jwt.MapClaims{
		"cap": string(code),
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, _ := tok.SignedString([]byte(config.SecretKey))
	c.JSON(http.StatusOK, gin.H{"token": signed, "svg": captchaSVG(string(code))})
}

// verifyCaptcha 验证 JWT 签名的验证码 token 和用户输入的答案是否匹配（大小写不敏感）。
func verifyCaptcha(token, answer string) bool {
	t, err := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) {
		return []byte(config.SecretKey), nil
	})
	if err != nil || !t.Valid {
		return false
	}
	claims, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return false
	}
	capCode, _ := claims["cap"].(string)
	return strings.ToUpper(answer) == capCode
}

// captchaSVG 生成带干扰线和噪点的 SVG 验证码图片。
func captchaSVG(code string) string {
	w, h := 160, 52
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" style="background:#ffffff;border-radius:8px;border:1px solid #e5e7eb">`, w, h)

	palettes := make([][3]int, len(captchaPalettes))
	copy(palettes, captchaPalettes)
	rand.Shuffle(len(palettes), func(i, j int) { palettes[i], palettes[j] = palettes[j], palettes[i] })

	for i := 0; i < 4; i++ {
		x1, y1 := rand.Intn(w/2), 5+rand.Intn(h-10)
		x2, y2 := w/2+rand.Intn(w/2), 5+rand.Intn(h-10)
		cx1, cy1 := 20+rand.Intn(w-40), rand.Intn(h)
		cx2, cy2 := 20+rand.Intn(w-40), rand.Intn(h)
		cc := 180 + rand.Intn(41)
		fmt.Fprintf(&b, `<path d="M%d,%d C%d,%d %d,%d %d,%d" stroke="rgb(%d,%d,%d)" stroke-width="1.5" fill="none" opacity="0.7"/>`,
			x1, y1, cx1, cy1, cx2, cy2, x2, y2, cc, cc, cc)
	}
	for i := 0; i < 30; i++ {
		x, y := rand.Intn(w), rand.Intn(h)
		cc := 150 + rand.Intn(51)
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="1.2" fill="rgb(%d,%d,%d)" opacity="0.6"/>`, x, y, cc, cc, cc)
	}
	step := (w - 20) / len(code)
	for i, ch := range code {
		x := 14 + i*step + rand.Intn(5) - 2
		y := 33 + rand.Intn(8)
		rot := rand.Intn(21) - 10
		p := palettes[i%len(palettes)]
		fmt.Fprintf(&b, `<text x="%d" y="%d" transform="rotate(%d,%d,%d)" font-family="Arial,Helvetica,sans-serif" font-size="26" font-weight="700" fill="rgb(%d,%d,%d)" letter-spacing="1">%c</text>`,
			x, y, rot, x, y, p[0], p[1], p[2], ch)
	}
	b.WriteString("</svg>")
	return b.String()
}

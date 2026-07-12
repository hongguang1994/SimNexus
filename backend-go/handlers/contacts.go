package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"simnexus-go/database"
	"simnexus-go/middleware"
	"simnexus-go/models"

	"github.com/gin-gonic/gin"
)

// 通讯录：每个用户私有，按 owner_id 隔离。

type contactIn struct {
	Name    string `json:"name"`
	Phone   string `json:"phone"`
	Company string `json:"company"`
	Note    string `json:"note"`
}

// ListContacts godoc
// @Summary 获取当前用户的通讯录
// @Tags 通讯录
// @Produce json
// @Success 200 {object} handlers.R{data=[]models.Contact}
// @Security BearerAuth
// @Router /api/v1/contacts/ [get]
func ListContacts(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var list []models.Contact
	database.DB.Where("owner_id = ?", me.ID).Order("name asc").Find(&list)
	OK(c, list)
}

// CreateContact 新建联系人。
func CreateContact(c *gin.Context) {
	me := middleware.CurrentUser(c)
	var body contactIn
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, 400, "参数错误")
		return
	}
	name := strings.TrimSpace(body.Name)
	phone := strings.TrimSpace(body.Phone)
	if name == "" && phone == "" {
		Fail(c, http.StatusBadRequest, 400, "姓名和号码不能同时为空")
		return
	}
	ct := models.Contact{
		OwnerID: me.ID, Name: name, Phone: phone,
		Company: strings.TrimSpace(body.Company), Note: strings.TrimSpace(body.Note),
	}
	if err := database.DB.Create(&ct).Error; err != nil {
		Fail(c, http.StatusInternalServerError, 500, "创建失败")
		return
	}
	OK(c, ct)
}

// UpdateContact 修改联系人（仅本人）。
func UpdateContact(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	var ct models.Contact
	if database.DB.Where("id = ? AND owner_id = ?", id, me.ID).First(&ct).Error != nil {
		Fail(c, http.StatusNotFound, 404, "联系人不存在")
		return
	}
	var body contactIn
	if err := c.ShouldBindJSON(&body); err != nil {
		Fail(c, http.StatusBadRequest, 400, "参数错误")
		return
	}
	ct.Name = strings.TrimSpace(body.Name)
	ct.Phone = strings.TrimSpace(body.Phone)
	ct.Company = strings.TrimSpace(body.Company)
	ct.Note = strings.TrimSpace(body.Note)
	if err := database.DB.Save(&ct).Error; err != nil {
		Fail(c, http.StatusInternalServerError, 500, "保存失败")
		return
	}
	OK(c, ct)
}

// DeleteContact 删除联系人（仅本人）。
func DeleteContact(c *gin.Context) {
	me := middleware.CurrentUser(c)
	id, _ := strconv.Atoi(c.Param("id"))
	res := database.DB.Where("id = ? AND owner_id = ?", id, me.ID).Delete(&models.Contact{})
	if res.RowsAffected == 0 {
		Fail(c, http.StatusNotFound, 404, "联系人不存在")
		return
	}
	OK(c, gin.H{"deleted": res.RowsAffected})
}

package main

import (
	"os"
	"strings"
	"unicode/utf8"
)

var translations = map[string]map[string]string{
	"en": {
		"connections": "Connections", "group": "Group", "add_session": "+ Add session", "add_group": "+ Add group", "settings": "Settings", "backup_restore": "Backup & Restore", "language_settings": "Language",
		"menu_help": "Enter select  |  Esc back/quit  |  j/k move  |  Shift+Up/Down reorder  |  e edit  |  d delete", "add_session_title": "Add session", "edit_session_title": "Edit session", "add_group_title": "Add group", "edit_group_title": "Edit group", "backup_title": "Backup settings",
		"name": "Name", "protocol": "Protocol (left/right)", "host": "Host or SSH alias", "user": "User (optional)", "port": "Port", "session_password": "Session password (optional)", "backup_dir": "Backup directory",
		"backup_interval": "Interval in hours (0 disables automatic backups)", "backup_max": "Maximum retained backups (0 keeps all)", "language": "Language (left/right)", "webdav_url": "WebDAV URL", "webdav_path": "WebDAV path", "webdav_user": "WebDAV username", "webdav_password": "WebDAV password", "restore_source": "Backup directory or vault.json",
		"init_script": "Initialization script (Enter to select)", "none": "None", "missing_script": "Missing script", "select_script": "Select initialization script", "new_script": "+ New script", "new_script_title": "New initialization script", "edit_script_title": "Edit initialization script", "script_name": "Script name", "interpreter": "Interpreter (left/right)", "script_content": "Script content", "edit_content": "Enter to edit", "save": "Save", "script_picker_help": "Enter select  |  e edit script  |  Esc back", "script_form_help": "Enter edits content or saves  |  Esc cancel",
		"form_help": "Left/right choose protocol  |  Enter next/save  |  Esc cancel", "backup_help": "Ctrl+B backup now  |  Ctrl+R restore  |  Enter save  |  Esc cancel", "settings_help": "Enter select  |  Esc back", "restore_confirm": "Replace the current encrypted vault with this backup?", "restore_help": "Enter/y restore  |  Esc/n cancel", "restored": "Backup restored.", "auto": "Automatic (system)", "zh": "Chinese", "en": "English",
		"session_saved": "Session saved.", "group_saved": "Group saved.", "script_saved": "Script saved.", "settings_saved": "Settings saved.", "backup_saved": "Backup saved.", "name_in_use": "A session or group with this name already exists here.", "delete_confirm": "Delete this item? Enter/y confirms, Esc/n cancels.", "delete_group_not_empty": "Delete the group's sessions and subgroups first.", "deleted": "Deleted.",
		"vault_location": "iShell stores its encrypted vault in ", "set_vault_password": "Set a vault password (leave blank to use the system credential store): ", "unlock_vault": "Vault password: ",
	},
	"zh": {
		"connections": "连接列表", "group": "分组", "add_session": "+ 添加会话", "add_group": "+ 添加分组", "settings": "设置", "backup_restore": "备份与恢复", "language_settings": "语言",
		"menu_help": "Enter 选择  |  Esc 返回/退出  |  j/k 移动  |  Shift+上下 调序  |  e 编辑  |  d 删除", "add_session_title": "添加会话", "edit_session_title": "编辑会话", "add_group_title": "添加分组", "edit_group_title": "编辑分组", "backup_title": "备份设置",
		"name": "名称", "protocol": "协议（左右方向键选择）", "host": "主机或 SSH 别名", "user": "用户（可选）", "port": "端口", "session_password": "会话密码（可选）", "backup_dir": "备份目录",
		"backup_interval": "备份间隔（小时，0 表示关闭自动备份）", "backup_max": "最多保留备份数（0 表示全部保留）", "language": "语言（左右方向键选择）", "webdav_url": "WebDAV 地址", "webdav_path": "WebDAV 路径", "webdav_user": "WebDAV 用户名", "webdav_password": "WebDAV 密码", "restore_source": "备份目录或 vault.json",
		"init_script": "初始化脚本（Enter 选择）", "none": "未设置", "missing_script": "脚本不存在", "select_script": "选择初始化脚本", "new_script": "+ 新建脚本", "new_script_title": "新建初始化脚本", "edit_script_title": "编辑初始化脚本", "script_name": "脚本名称", "interpreter": "解释器（左右方向键选择）", "script_content": "脚本内容", "edit_content": "Enter 编辑内容", "save": "保存", "script_picker_help": "Enter 选择  |  e 编辑脚本  |  Esc 返回", "script_form_help": "Enter 编辑内容或保存  |  Esc 取消",
		"form_help": "左右方向键选择协议  |  Enter 下一项/保存  |  Esc 取消", "backup_help": "Ctrl+B 立即备份  |  Ctrl+R 恢复  |  Enter 保存  |  Esc 取消", "settings_help": "Enter 选择  |  Esc 返回", "restore_confirm": "确认用以下备份覆盖当前加密 vault？", "restore_help": "Enter/y 恢复  |  Esc/n 取消", "restored": "备份已恢复。", "auto": "自动（跟随系统）", "zh": "中文", "en": "English",
		"session_saved": "会话已保存。", "group_saved": "分组已保存。", "script_saved": "脚本已保存。", "settings_saved": "设置已保存。", "backup_saved": "备份已保存。", "name_in_use": "当前分组内已存在同名会话或分组。", "delete_confirm": "删除该项目？Enter/y 确认，Esc/n 取消。", "delete_group_not_empty": "请先删除该分组中的会话和子分组。", "deleted": "已删除。",
		"vault_location": "iShell 的加密数据目录：", "set_vault_password": "设置 vault 密码（留空则使用系统凭证存储）：", "unlock_vault": "Vault 密码：",
	},
}

func systemLanguage() string {
	locale := strings.ToLower(platformLocale() + " " + os.Getenv("LC_ALL") + " " + os.Getenv("LC_MESSAGES") + " " + os.Getenv("LANG"))
	if strings.Contains(locale, "zh") {
		return "zh"
	}
	return "en"
}

func (m model) language() string {
	if m.settings.Language == "zh" || m.settings.Language == "en" {
		return m.settings.Language
	}
	return systemLanguage()
}

func (m model) tr(key string) string {
	return translate(m.language(), key)
}

func translate(language, key string) string {
	if value := translations[language][key]; value != "" {
		return value
	}
	return translations["en"][key]
}

func displayWidth(value string) int {
	width := 0
	for _, r := range value {
		if (r >= 0x2e80 && r <= 0xa4cf) || (r >= 0xac00 && r <= 0xd7a3) || (r >= 0xf900 && r <= 0xfaff) || (r >= 0xff01 && r <= 0xff60) {
			width += 2
		} else {
			width++
		}
	}
	return width
}

func padDisplay(value string, width int) string {
	return value + strings.Repeat(" ", width-displayWidth(value))
}

func mask(value string) string {
	return strings.Repeat("*", utf8.RuneCountInString(value))
}

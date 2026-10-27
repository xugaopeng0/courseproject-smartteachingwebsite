package main

import (
	"net/http"
)

// GET /api/tool-servers：只列出当前教师自己的服务器（含发现的工具与 has_token）。
func (a *App) listToolServersHandler(w http.ResponseWriter, r *http.Request, u *User) error {
	if err := requireTeacher(u); err != nil {
		return err
	}
	list, err := a.listToolServers(r.Context(), u.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"servers": list})
	return nil
}

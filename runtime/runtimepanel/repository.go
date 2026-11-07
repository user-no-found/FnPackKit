package runtimepanel

// RebuildLocalCommand applies current local edits without fetching remote code.
func RebuildLocalCommand() Command {
	return Command{ID: "rebuild-local", Label: "应用本地修改", Confirm: true,
		Warning: "将构建并验证当前本地代码；正在运行的后端会重启，已停止的后端保持停止。是否继续？"}
}

// UpdateBackendCommand explicitly fetches the configured repository branch.
func UpdateBackendCommand() Command {
	return Command{ID: "update-backend", Label: "更新后端", Confirm: true,
		Warning: "将获取指定分支的远端代码，构建验证后重启正在运行的后端。有未提交修改时拒绝更新，已停止的后端保持停止。是否继续？"}
}

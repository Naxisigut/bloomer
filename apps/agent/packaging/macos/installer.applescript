on run
	set packagePath to (POSIX path of (path to me)) & "Contents/Resources/package"
	try
		with timeout of 600 seconds
			do shell script (quoted form of (packagePath & "/bloomerctl")) & " install --package-dir " & quoted form of packagePath
		end timeout
		display dialog "Bloomer 已安装并启动，登录后将自动运行。\n\n管理工具位于：\n~/Library/Application Support/Bloomer/agent/management/bloomerctl\n\n现在可以推出此磁盘镜像。" with title "Bloomer" buttons {"完成"} default button "完成" with icon note
	on error errorMessage number errorNumber
		if errorNumber is not -128 then
			display dialog "安装未完成：\n\n" & errorMessage & "\n\n安装阶段记录：\n~/Library/Application Support/Bloomer/agent/install-progress.json\n\n请按上方提示处理后重新运行安装器。" with title "Bloomer" buttons {"关闭"} default button "关闭" with icon stop
		end if
	end try
end run

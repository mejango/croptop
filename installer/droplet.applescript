-- Croptop's Dock presence. While this app is open the console and the IPFS
-- node run; quit it and they stop. Drop images, video or audio on the icon to
-- start a post; click it to open the console.
property console : "http://127.0.0.1:8086"

on cli()
	return quoted form of POSIX path of (path to resource "croptop")
end cli

-- start the console if it is not answering, and wait until it is
on ensureNode()
	do shell script "if ! curl -s -m 2 " & console & "/v0/ping >/dev/null; then " & ¬
		"mkdir -p \"$HOME/Library/Application Support/croptop\"; " & ¬
		cli() & " serve --no-open >>\"$HOME/Library/Application Support/croptop/app.log\" 2>&1 & " & ¬
		"for i in $(seq 1 60); do curl -s -m 2 " & console & "/v0/ping >/dev/null && break; sleep 0.5; done; fi"
end ensureNode

on run
	-- the app owns the node from now on; a login service would fight it for the port
	try
		do shell script cli() & " service uninstall >/dev/null 2>&1"
	end try
	ensureNode()
	do shell script "open " & console & "/"
end run

on open theFiles
	ensureNode()
	set args to ""
	repeat with f in theFiles
		set args to args & " " & quoted form of POSIX path of f
	end repeat
	do shell script cli() & " post" & args & " >/dev/null 2>&1 &"
end open

on reopen
	ensureNode()
	do shell script "open " & console & "/"
end reopen

on idle
	ensureNode()
	return 15
end idle

on quit
	try
		do shell script "curl -s -m 3 -X POST " & console & "/v0/croptop/quit >/dev/null"
	end try
	continue quit
end quit

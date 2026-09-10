-- Croptop's Dock presence: drop images, video or audio on the icon to start a
-- post; click it to open the console. The console itself runs as a login
-- service (croptop service install), so the IPFS node stays on.
on croptop()
	return quoted form of POSIX path of (path to resource "croptop")
end croptop

on ensureService()
	try
		do shell script croptop() & " service install >/dev/null 2>&1"
	end try
end ensureService

on run
	ensureService()
	do shell script "open http://127.0.0.1:8086/"
end run

on open theFiles
	ensureService()
	set args to ""
	repeat with f in theFiles
		set args to args & " " & quoted form of POSIX path of f
	end repeat
	do shell script croptop() & " post" & args & " >/dev/null 2>&1 &"
end open

on reopen
	do shell script "open http://127.0.0.1:8086/"
end reopen

on idle
	return 60
end idle

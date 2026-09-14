from pathlib import Path
import hashlib
root=Path(__file__).parent
css=(root/'download.css').read_text();js=(root/'download.js').read_text()
release='https://github.com/mejango/croptop/releases/'
cards=''
for os,title,note,href in [('mac','Mac','Apple Silicon + Intel',release+'latest/download/Croptop.dmg'),('linux','Linux','Ubuntu / Debian',release+'download/v0.11.0/croptop_0.11.0_linux_amd64.deb'),('windows','Windows','',release+'latest/download/croptop-setup-amd64.exe')]:
    options=''
    if os=='linux':options+='<div class="ct-download-group" role="group" aria-label="Linux package"><button type="button" data-format="deb" aria-pressed="true">DEB</button><button type="button" data-format="rpm" aria-pressed="false">RPM</button></div>'
    if os!='mac':options+='<div class="ct-download-group" role="group" aria-label="'+title+' processor"><button type="button" data-arch="amd64" aria-pressed="true">Intel / AMD</button><button type="button" data-arch="arm64" aria-pressed="false">ARM</button></div>'
    cards+=f'<div class="ct-download-card" data-os="{os}"><h2>{title}</h2><canvas width="260" height="200" aria-hidden="true"></canvas><div class="ct-download-options">{options}<p class="ct-download-note">{note}</p></div><a class="ct-download-link" href="{href}">Download</a></div>'
body='<section class="ct-download" aria-label="Download Croptop"><div class="ct-download-grid">'+cards+'</div></section>'
source='<div class="ct-download-source"><a href="source.html" download>Source</a></div>'
version=hashlib.sha256(js.encode()).hexdigest()[:12]
(root/'post.md').write_text('<style>'+css+'</style>\n\n'+body+f'\n\n<script src="download.js?v={version}"></script>\n\n'+(root/'install.md').read_text()+'\n\n'+source)
# Standalone downloadable source includes the cards and the installation text.
import html
(root/'source.html').write_text('<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Get Croptop</title><style>body{background:#171717;color:#eee;font-family:system-ui;max-width:1000px;margin:40px auto;padding:0 20px}pre{white-space:pre-wrap;line-height:1.6}'+css+'</style>'+body+'<pre>'+html.escape((root/'install.md').read_text())+'</pre><script>'+js+'</script></html>')

import base64
import html
import hashlib
import json
from pathlib import Path
root=Path(__file__).parent
css=(root/'welcome.css').read_text()
drawing=(root/'drawing.js').read_text()
(root/'welcome.js').write_text((root/'welcome-template.js').read_text().replace('ILLUSTRATIONS',drawing).replace('AUDIO_PLAYER',(root/'audio-player.js').read_text()).replace('RHYTHM',(root/'rhythm.js').read_text()).replace('BOTS',(root/'bots.js').read_text()).replace('EMBEDS',(root/'embeds.js').read_text()))
ids={'start':'9EE5FA3A-F992-47A9-BDBB-B38F525FDAD5','studio':'71E4547B-9F51-490B-B8CB-7FEA4FBF9DA7','faq':'35C0796C-B6AC-4613-AB17-56083B645539','art':'D4C68DAB-4CB6-4B26-B157-FC8CC7D54A50','network':'EDAA9F21-873F-47E2-B5EB-689003FB9B47'}
ids.update({'writing': '9CB0B19C-061C-4378-9D27-9E434C796B76', 'music': 'C7215628-0976-4CC6-AC20-7950C93A83F3', 'video': '77E6E2AE-B847-4BF2-B904-93E59021712E', 'audio': '394192CB-EC08-44C2-8D8D-9A7065D333B8', 'film': '50A1BEF8-7D31-48C5-AC60-F4A47C2E6480'})
ids['bots']='D174A1AD-E36E-4F81-AE29-93107150749D'
ids.update({'essays':'E2F5F17C-3088-48BB-8440-DA3E357F5104','documents':'E94F0B91-BD34-4B70-A88C-CC057B7A0D7B'})
app='../9E6717AF-A8EF-4475-8FC8-CB3BC64E7846/'
def source():return '<div class="ct-source"><a href="source.html" download>Source</a></div>'
def uri(filename,mime):return 'data:'+mime+';base64,'+base64.b64encode((root/'media'/filename).read_bytes()).decode()
audio_uri=uri('small-hours.m4a','audio/mp4')
video_uri=uri('passing-light.mp4','video/mp4')
poster_uri=uri('passing-light.png','image/png')
radio_uri=uri('croptop-radio.m4a','audio/mp4')
film_uri=uri('paper-sun.mp4','video/mp4')
film_poster_uri=uri('paper-sun.jpg','image/jpeg')
waveform=json.loads((root/'media'/'waveform.json').read_text())
radio_waveform=json.loads((root/'media'/'radio-waveform.json').read_text())
def wave(values):return '<svg viewBox="0 0 640 140" aria-hidden="true">'+''.join(f'<line x1="{5+i*10}" x2="{5+i*10}" y1="{70-max(2,v*55)}" y2="{70+max(2,v*55)}" stroke="currentColor" stroke-width="2"/>' for i,v in enumerate(values))+'</svg>'

posts=[
 {'key':'start','title':'Start here','label':'Start','tag':'start here','body':f'''
<div class="ct-start">
<div class="ct-start-copy"><h2>Make a site.<br>Add a post.<br>Publish to peers.</h2><p>Words, sound, video,<br>and your own code.</p><a class="ct-button ct-primary" href="{app}">Get Croptop</a><p class="ct-start-note">No account needed.</p></div>
<div class="ct-start-sample"><canvas data-start-art aria-hidden="true"></canvas><div class="ct-start-choices" role="group" aria-label="Explore post types"><button data-start-kind="writing" aria-pressed="true">Words</button><button data-start-kind="music" aria-pressed="false">Sound</button><button data-start-kind="video" aria-pressed="false">Video</button><button data-start-kind="studio" aria-pressed="false">Code</button></div><a data-start-link href="../{ids['writing']}/">Open example</a></div>
</div>
'''+source()},
 {'key':'studio','title':'Custom','label':'Custom','tag':'custom','body':'<div class="ct-instrument"><h2>Make a loop.</h2><p>Tap a pattern. Press play.</p><div data-rhythm></div></div>'+source()},
 {'key':'art','title':'Embeds','label':'Embeds','tag':'custom','body':(root/'content/embeds.html').read_text()+source()},
 {'key':'network','title':'Peers','label':'Peers','tag':'how it works','body':'''<p class="ct-lead">Help preserve the content you follow.</p><div class="ct-demo"><canvas aria-label="Peer web. Drag or use arrow keys to rotate."></canvas><div class="ct-controls"><button data-add>Add reader +</button><button data-remove>Remove −</button><button data-offline aria-pressed="false">Go offline</button><button data-pause>Pause motion</button></div><div class="ct-status" data-status role="status"></div></div>'''+source()},
 {'key':'faq','title':'Basics','label':'Basics','tag':'start here','body':'''<div class="ct-answer"><h3>Where does it live?</h3><p>Publish to peers through IPFS. Followers of sites keep copies of them.</p></div><div class="ct-answer"><h3>Updates?</h3><p>Edit and republish any time. Old versions remain on the web, but your main website always goes to your latest.</p></div><div class="ct-answer"><h3>My own domain?</h3><p>Yes, any ENS or .com, etc.</p></div><div class="ct-answer"><h3>Wallet?</h3><p>Only if you want to buy something. No wallet needed to post, content is peer-to-peer not blockchain.</p></div><div class="ct-answer"><h3>All devices?</h3><p>Run Croptop on Mac, Windows, or Linux, a small app that hosts the peer-to-peer content and makes posting easy. Read anywhere. Your phone can connect to your node to post.</p></div><div class="ct-answer"><h3>Help?</h3><p><a href="https://github.com/mejango/croptop">GitHub</a> · <button class="ct-help-copy" data-help-copy>Copy prompt for your bot</button><span class="ct-help-status" data-help-status role="status"></span></p></div>'''+source()}
]
posts[1:1]=[
 {'key':'essays','title':'Essays','label':'Essays','tag':'posts','body':(root/'content/essay.html').read_text()+source()},
 {'key':'documents','title':'Documents','label':'Documents','tag':'posts','body':(root/'content/technical.html').read_text()+source()},
 {'key':'writing','title':'Poetry','label':'Poetry','tag':'posts','body':'<div class="ct-writing"><h2>First bloom</h2><p>The wind nudges<br>each sleeping bud.<br><br>The tree answers<br>in flowers.</p></div>'+source()},
 {'key':'music','title':'Music','label':'Music','tag':'posts','media':audio_uri,'mediaLabel':'Small hours, a twelve-second loop','waveform':waveform,'body':f'<h2>Small hours</h2><div class="ct-audio-wave">{wave(waveform)}</div><audio class="ct-media" controls preload="metadata" aria-label="Small hours, a twelve-second audio loop" src="{audio_uri}"></audio>'+source()},
 {'key':'audio','title':'Audio','label':'Audio','tag':'posts','media':radio_uri,'mediaLabel':'Croptop Radio, a twelve-second spoken intro','waveform':radio_waveform,'body':f'<h2>Croptop Radio</h2><p>Episode one. A small show about the web you own.</p><div class="ct-audio-wave">{wave(radio_waveform)}</div><audio class="ct-media" controls preload="metadata" aria-label="Croptop Radio, a twelve-second spoken intro" src="{radio_uri}"></audio>'+source()},
 {'key':'video','title':'Video','label':'Video','tag':'posts','media':video_uri,'mediaLabel':'Passing light, an eight-second silent video','poster':poster_uri,'body':f'<video class="ct-media" controls playsinline preload="metadata" poster="{poster_uri}" aria-label="Passing light, an eight-second silent video" src="{video_uri}"></video>'+source()},
 {'key':'film','title':'Film','label':'Film','tag':'posts','media':film_uri,'mediaLabel':'Paper Sun, a twelve-second film with sound','poster':film_poster_uri,'body':f'<h2>Paper Sun</h2><p>A Croptop film. Twelve seconds, with sound.</p><video class="ct-media" controls playsinline preload="metadata" poster="{film_poster_uri}" aria-label="Paper Sun, a twelve-second letterboxed film with sound" src="{film_uri}"></video>'+source()}
]

posts.append({'key':'bots','title':'Bots','label':'Bots','tag':'start here','body':(root/'content/bots.html').read_text()+source()})
order=['start','essays','music','documents','video','writing','audio','film','studio','art','bots','network','faq']
posts.sort(key=lambda post:order.index(post['key']))

preview=(root/'preview-template.js').read_text().replace('ILLUSTRATIONS',drawing).replace('RHYTHM',(root/'rhythm.js').read_text())
script_version=hashlib.sha256((root/'welcome.js').read_bytes()).hexdigest()[:12]
for n,p in enumerate(posts):
 p['id']=ids[p['key']];p['order']=n
 preview_code=preview.replace('CONFIG',json.dumps({k:p[k] for k in ['key','label','order','media','mediaLabel','waveform','poster'] if k in p}))
 body=f'<style>{css}</style>\n<script type="croptop/preview">{preview_code}</script>\n<section class="ct-welcome" data-demo="{p["key"]}">{p["body"]}</section>\n<script src="welcome.js?v={script_version}"></script>'
 path=root/'posts'/p['key'];path.mkdir(parents=True,exist_ok=True)
 (path/'post.html').write_text(body)
 # A single portable file: inline the playground and use public URLs for links.
 portable_body=p['body'].replace(source(),'').replace('href="../','href="https://crop.top/')
 portable=f'''<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{html.escape(p['title'])} — Croptop</title>
<style>body{{background:#171717;color:#eee;--foreground-color:#eee;--font-family:system-ui,sans-serif;max-width:800px;margin:40px auto;padding:0 24px}}{css}</style></head><body>
<section class="ct-welcome" data-demo="{p['key']}">{portable_body}</section>
<script>{(root/'welcome.js').read_text()}</script></body></html>'''
 (path/'source.html').write_text(portable)
 (path/'preview.js').write_text(preview_code)
 p.pop('body')
 for field in ['media','mediaLabel','waveform','poster']:p.pop(field,None)
(root/'manifest.json').write_text(json.dumps(posts,indent=2))

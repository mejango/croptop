/* A self-contained Croptop post playground. No dependencies. Embeds request an opt-in manifest. */
(() => {
  ILLUSTRATIONS
  AUDIO_PLAYER
  RHYTHM
  BOTS
  EMBEDS
  const root = document.currentScript.previousElementSibling;
  if (!root || !root.matches('.ct-welcome')) return;
  const scriptURL = document.currentScript.src || document.baseURI;
  root.querySelectorAll('a[href]').forEach(a => { if (!/^([a-z]+:|#|\/)/i.test(a.getAttribute('href'))) a.href = new URL(a.getAttribute('href'), scriptURL).href; });
  const $ = s => root.querySelector(s);
  const mode = root.dataset.demo;
  root.querySelectorAll('audio,video').forEach(media => media.addEventListener('play', () => document.querySelectorAll('audio,video').forEach(other => { if (other !== media) other.pause(); })));
  const download = (blob, filename) => {
    const url = URL.createObjectURL(blob), a = document.createElement('a');
    a.href = url; a.download = filename; a.click(); setTimeout(() => URL.revokeObjectURL(url), 30000);
  };
  if (mode === 'start') {
    const canvas=$('[data-start-art]'), ctx=canvas.getContext('2d');
    const examples={writing:'9CB0B19C-061C-4378-9D27-9E434C796B76',music:'C7215628-0976-4CC6-AC20-7950C93A83F3',video:'77E6E2AE-B847-4BF2-B904-93E59021712E',studio:'71E4547B-9F51-490B-B8CB-7FEA4FBF9DA7'};
    canvas.width=600;canvas.height=440;
    const show=kind=>{ctx.clearRect(0,0,600,440);ctx.save();ctx.translate(80,0);drawCroptopArt(ctx,{kind,width:440,height:440});ctx.restore();$('[data-start-link]').href=new URL('../'+examples[kind]+'/',scriptURL).href;root.querySelectorAll('[data-start-kind]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.startKind===kind)));};
    root.querySelectorAll('[data-start-kind]').forEach(b=>b.onclick=()=>show(b.dataset.startKind));show('writing');
  }
  if (mode === 'faq') {
    $('[data-help-copy]').onclick=async()=>{
      const prompt='Help me with Croptop. First ask what I am trying to do and what went wrong. Check the installed app or CLI version, inspect relevant local settings and logs if you have access, and consult the current documentation at https://github.com/mejango/croptop. Preserve my sites, posts, and publishing keys. Never ask me to paste keys or passcodes into chat. Diagnose the issue, make reversible fixes within my request, and verify the result. If you cannot use my computer, give me short steps I can follow. If the issue needs a bug report, prepare one with reproduction steps and redacted details for me to review.';
      let copied=false;try{await navigator.clipboard.writeText(prompt);copied=true;}catch{}
      if(!copied){let field=$('[data-help-prompt]');if(!field){field=document.createElement('textarea');field.dataset.helpPrompt='';field.readOnly=true;field.setAttribute('aria-label','Help prompt for your bot');$('[data-help-status]').after(field);}field.value=prompt;field.focus();field.select();try{copied=document.execCommand('copy');}catch{}if(copied)field.remove();}
      $('[data-help-status]').textContent=copied?'Copied. Paste it into your bot.':'Prompt selected. Use your device’s Copy command.';
    };
  }
  if (mode === 'art') mountCroptopEmbed(root);
  if (mode === 'bots') mountCroptopBots(root);
  if (mode === 'studio') mountCroptopRhythm($('[data-rhythm]'));
  if (mode !== 'network') { root.dataset.ready = 'true'; return; }
  const canvas = $('canvas'), ctx = canvas.getContext('2d');
  if (!ctx) { $('[data-status]').textContent = 'Canvas unavailable in this browser.'; return; }
  const reduced = matchMedia('(prefers-reduced-motion: reduce)').matches;
  let running = !reduced, phase = 0, width = 0, height = 0, pointer = .5, frame = 0, last = 0;
  let garden=createCroptopGarden();
  let density = 11, color = '#171712', readers = 3, online = true, yaw = 0, pitch = 0;
  const size = () => {
    const b = canvas.getBoundingClientRect(), dpr = Math.min(devicePixelRatio || 1, 2);
    width = Math.max(1,b.width); height = Math.max(1,b.height);
    canvas.width = Math.round(width*dpr); canvas.height = Math.round(height*dpr);ctx.setTransform(dpr,0,0,dpr,0,0);
    draw();
  };
  const draw = () => {
    ctx.fillStyle='#ead9b6';ctx.fillRect(0,0,width,height);
    if(mode==='art')drawCroptopGarden(ctx,{width,height,phase,garden,density});
    else drawCroptopArt(ctx,{kind:'network',width,height,phase,density,peers:readers,online,yaw,pitch});
  };
  const tick = t => {
    if(!root.isConnected){observer.disconnect();return;}
    if(running && !document.hidden && root.getClientRects().length && t-last>32){phase+=.027;draw();last=t;}
    frame=requestAnimationFrame(tick);
  };
  const observer = new ResizeObserver(size);observer.observe(canvas);
  $('[data-pause]').textContent=running?'Pause motion':'Play motion';
  $('[data-pause]').onclick=()=>{running=!running;$('[data-pause]').textContent=running?'Pause motion':'Play motion';};
  if(mode==='art'){
    $('[data-regrow]').onclick=()=>{garden=createCroptopGarden();phase=0;draw();};
    $('[name=density]').oninput=e=>{density=+e.target.value;$('output').textContent=density;draw();};
    canvas.onpointermove=e=>{pointer=(e.clientX-canvas.getBoundingClientRect().left)/width;draw();};
    root.querySelectorAll('[data-color]').forEach(b=>b.onclick=()=>{color=b.dataset.color;root.querySelectorAll('[data-color]').forEach(x=>x.setAttribute('aria-pressed',String(x===b)));draw();});
    $('[data-save]').onclick=()=>{draw();canvas.toBlob(blob=>{if(blob)download(blob,'croptop-garden.png');},'image/png');$('[data-status]').textContent='Image saved.';};
  }else{
    canvas.style.touchAction='none';canvas.style.cursor='grab';canvas.tabIndex=0;
    let drag=null;
    canvas.onpointerdown=e=>{drag={x:e.clientX,y:e.clientY};canvas.setPointerCapture(e.pointerId);canvas.style.cursor='grabbing';};
    canvas.onpointermove=e=>{if(!drag)return;yaw+=(e.clientX-drag.x)*.008;pitch=Math.max(-1.3,Math.min(1.3,pitch+(e.clientY-drag.y)*.008));drag={x:e.clientX,y:e.clientY};draw();};
    canvas.onpointerup=canvas.onpointercancel=()=>{drag=null;canvas.style.cursor='grab';};
    canvas.onkeydown=e=>{if(!['ArrowLeft','ArrowRight','ArrowUp','ArrowDown'].includes(e.key))return;e.preventDefault();if(e.key==='ArrowLeft')yaw-=.15;if(e.key==='ArrowRight')yaw+=.15;if(e.key==='ArrowUp')pitch=Math.max(-1.3,pitch-.15);if(e.key==='ArrowDown')pitch=Math.min(1.3,pitch+.15);draw();};
    const update=()=>{
      canvas.setAttribute('aria-label',`${readers+1} peers in a 3D web. Drag or use arrow keys to rotate.`);

      $('[data-offline]').setAttribute('aria-pressed',String(!online));
      $('[data-offline]').textContent=online?'Go offline':'Go online';
      $('[data-remove]').disabled=readers===0;$('[data-add]').disabled=readers===96;
      $('[data-status]').textContent=online?`${readers + 1} copies online.`:readers?`${readers} reader ${readers === 1 ? 'copy' : 'copies'} online. Yours is offline.`:'No copies online.';
      draw();
    };
    $('[data-add]').onclick=()=>{readers=Math.min(96,readers+1);update();};
    $('[data-remove]').onclick=()=>{readers=Math.max(0,readers-1);update();};
    $('[data-offline]').onclick=()=>{online=!online;update();};update();
  }
  size();frame=requestAnimationFrame(tick);root.dataset.ready = 'true';
})();

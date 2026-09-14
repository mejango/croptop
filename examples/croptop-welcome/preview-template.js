export default function(el){
 ILLUSTRATIONS
 RHYTHM
 const cfg=CONFIG,small=el.clientHeight<140,ink='#171712',paper='#f3e4c4';
 const backgrounds={start:'#d5b574',writing:'#ead9b6',essays:'#dfc592',documents:'#e4dfcd',audio:'#de7669',video:'#3977d8',studio:'#d59b50',art:'#dfc592',network:'#b4bd99',faq:'#d9b77c',bots:'#dfc592'};
 el.innerHTML='';const box=document.createElement('div');box.className='ct-preview';box.dataset.preview=cfg.key;
 box.style.cssText=`position:absolute;inset:0;background:${backgrounds[cfg.key]};color:${ink};overflow:hidden;`;
 const style=document.createElement('style');style.textContent=`
 .ct-preview *{box-sizing:border-box}.ct-preview .ct-preview-footer{position:absolute;left:24px;right:24px;bottom:18px;display:flex;align-items:center;justify-content:space-between;gap:12px;min-height:36px}
 .ct-preview .ct-preview-label{font:600 17px var(--font-family,system-ui);color:#171712}.ct-preview .ct-preview-choices{display:flex;gap:4px;align-items:center}
 .ct-preview button{display:grid;place-items:center;width:36px;height:36px;padding:0;border:1.5px solid transparent;border-radius:3px 2px 4px 2px;background:transparent;color:#171712;font:16px/1 var(--font-family,system-ui);cursor:pointer}
 .ct-preview button:hover{border-color:#17171288}.ct-preview button[aria-pressed=true]{background:#171712;color:#f3e4c4}.ct-preview button:disabled{opacity:.3;cursor:default}.ct-preview :focus-visible{outline:2px solid #171712;outline-offset:3px}
 .ct-preview input[type=range]{width:84px;accent-color:#171712;cursor:ew-resize}.ct-preview svg{width:16px;height:16px;fill:none;stroke:currentColor;stroke-width:1.5}
 .ct-preview .ct-preview-writing{position:absolute;inset:28px 30px 70px;display:flex;flex-direction:column;justify-content:center;font:16px/1.65 Georgia,serif;letter-spacing:-.02em;color:#171712}.ct-preview .ct-preview-writing strong{font-weight:400;font-size:26px;line-height:1.2;margin-bottom:16px}
 .ct-preview .ct-preview-document span{font:14px/1.5 var(--font-family,system-ui)}.ct-preview .ct-preview-document b{display:block;font-size:11px;margin:14px 0 4px;text-transform:uppercase;letter-spacing:.06em}.ct-preview .ct-preview-essay span{font-size:15px;line-height:1.5}.ct-preview .ct-preview-essay-note{margin-top:12px}.ct-preview{container-type:inline-size}@container(max-width:300px){.ct-preview .ct-preview-writing{inset:18px 20px 58px}.ct-preview .ct-preview-writing strong{font-size:20px;margin-bottom:10px}.ct-preview .ct-preview-essay span,.ct-preview .ct-preview-document span{font-size:12px;line-height:1.4}.ct-preview .ct-preview-essay-note{margin-top:8px}.ct-preview .ct-preview-document b{margin-top:8px}}
 .ct-preview .ct-preview-video{position:absolute;left:8%;top:8%;width:84%;height:66%;object-fit:contain;background:#171712;border:3px solid #171712;border-radius:4px}
 `;box.appendChild(style);
 const texture=document.createElement('canvas');texture.width=texture.height=96;const tx=texture.getContext('2d'),pixels=tx.createImageData(96,96);for(let i=0;i<pixels.data.length;i+=4){const n=Math.sin(i*12.9898)*43758.5453;const dark=n-Math.floor(n)>.5;pixels.data[i]=pixels.data[i+1]=pixels.data[i+2]=dark?0:255;pixels.data[i+3]=Math.round((n-Math.floor(n))*12)}tx.putImageData(pixels,0,0);
 const grain=document.createElement('div');grain.style.cssText=`position:absolute;inset:0;background-image:url(${texture.toDataURL()});pointer-events:none;opacity:.65`;box.appendChild(grain);
 const canvas=document.createElement('canvas');canvas.setAttribute('aria-hidden','true');canvas.style.cssText='position:absolute;inset:0;width:100%;height:100%';box.appendChild(canvas);
 const footer=document.createElement('div');footer.className='ct-preview-footer';const label=document.createElement('span');label.className='ct-preview-label';label.textContent=cfg.label;footer.appendChild(label);box.appendChild(footer);
 if(small){footer.style.cssText='left:10px;right:10px;bottom:5px;min-height:18px';label.style.fontSize='12px'}el.appendChild(box);
 const ctx=canvas.getContext('2d');if(!ctx)return;
 const reduced=matchMedia('(prefers-reduced-motion: reduce)').matches;
 let w=0,h=0,phase=.4,last=0,visible=true,running=!reduced,layout='grid',poster='symbol',density=12,peers=4,media;
 const draw=()=>{
  if(!w||!h)return;ctx.clearRect(0,0,w,h);
  const area=h-(small?22:68),size=Math.min(w*.77,area*.95),x=(w-size)/2,y=(area-size)/2+3;
  if(cfg.key==='studio'||(['writing','essays','documents'].includes(cfg.key)&&!small))return;
  if(cfg.key==='studio'&&poster==='type'){
   ctx.save();ctx.translate(w/2,area/2);ctx.rotate(-.025);ctx.fillStyle=paper;ctx.fillRect(-size*.36,-size*.43,size*.72,size*.86);ctx.strokeStyle=ink;ctx.lineWidth=3;ctx.strokeRect(-size*.36,-size*.43,size*.72,size*.86);ctx.fillStyle=ink;ctx.textAlign='center';ctx.font=`${size*.23}px Georgia,serif`;ctx.fillText('hello.',0,size*.06);ctx.restore();return;
  }
  ctx.save();ctx.translate(x,y);drawCroptopArt(ctx,{kind:cfg.key==='bots'?'art':cfg.key==='art'?'site':cfg.key,width:size,height:size,phase,density,peers,layout,face:cfg.key==='bots'?(density-8+Math.floor(phase*2))%7:null});ctx.restore();
  if(cfg.key==='audio'&&!small){
   const bars=cfg.waveform||[],progress=media&&Number.isFinite(media.duration)?media.currentTime/media.duration:0;
   bars.forEach((v,i)=>{ctx.strokeStyle=i/bars.length<progress?ink:'#17171255';ctx.lineWidth=2;ctx.beginPath();const xx=w*.20+i/bars.length*w*.6,yy=area-1;ctx.moveTo(xx,yy-v*9);ctx.lineTo(xx,yy+v*9);ctx.stroke()});
  }
 };
 const choices=document.createElement('div');choices.className='ct-preview-choices';choices.addEventListener('click',e=>e.stopPropagation());choices.addEventListener('keydown',e=>e.stopPropagation());
 const icons={grid:'<rect x="2" y="2" width="5" height="5"/><rect x="11" y="2" width="5" height="5"/><rect x="2" y="11" width="5" height="5"/><rect x="11" y="11" width="5" height="5"/>',list:'<path d="M2 3h14M2 9h14M2 15h14"/>',symbol:'<path d="M9 1v16M1 9h16M3 3l12 12M3 15L15 3"/>',pause:'<path d="M6 3v12M12 3v12"/>',play:'<path d="m6 3 8 6-8 6z"/>'};
 const setIcon=(b,icon)=>b.innerHTML=`<svg viewBox="0 0 18 18" aria-hidden="true">${icons[icon]}</svg>`;
 const button=(name,content,action)=>{const b=document.createElement('button');b.type='button';b.title=name;b.setAttribute('aria-label',name);if(icons[content])setIcon(b,content);else b.textContent=content;b.onclick=action;choices.appendChild(b);return b;};
 if(cfg.key==='studio'){const instrument=document.createElement('div');instrument.style.cssText='position:absolute;left:24px;right:24px;top:22px;bottom:64px;display:flex;align-items:center';const inner=document.createElement('div');instrument.appendChild(inner);box.appendChild(instrument);mountCroptopRhythm(inner,true);if(small)instrument.style.display='none';}
 if(!small){
  footer.appendChild(choices);
  if(cfg.key==='bots'){
   const slider=document.createElement('input');slider.type='range';slider.min=8;slider.max=18;slider.step=1;slider.value=density;slider.setAttribute('aria-label','Petals');slider.oninput=()=>{density=+slider.value;draw()};choices.appendChild(slider);
   const pause=button(running?'Pause animation':'Play animation',running?'pause':'play',()=>{running=!running;setIcon(pause,running?'pause':'play');pause.title=running?'Pause animation':'Play animation';pause.setAttribute('aria-label',pause.title)});
  }else if(cfg.key==='network'){
   const update=()=>{minus.disabled=peers<=2;plus.disabled=peers>=96;canvas.setAttribute('aria-label',`Illustration: ${peers} peers keeping copies`);draw()};const minus=button('Remove peer','−',()=>{peers=Math.max(2,peers-1);update()});const plus=button('Add peer','+',()=>{peers=Math.min(96,peers+1);update()});update();
  }else if(cfg.key==='essays'){
   const text=document.createElement('div');text.className='ct-preview-writing ct-preview-essay';text.innerHTML='<strong>Croptop TLDR</strong><span>Self-serve websites with content feeds and revenue streams baked in.</span><span class="ct-preview-essay-note">You’re looking at it now. This site is a Croptop site.</span>';box.appendChild(text);
  }else if(cfg.key==='documents'){
   const text=document.createElement('div');text.className='ct-preview-writing ct-preview-document';text.innerHTML='<strong>How Croptop works</strong><span><b>Architecture</b>Local files. A publishing key. A network of copies.<b>Technical guide</b>From your first post to the peers that keep it.</span>';box.appendChild(text);
  }else if(cfg.key==='writing'){
   const text=document.createElement('div');text.className='ct-preview-writing';text.innerHTML='<strong>First bloom</strong><span>The wind nudges<br>each sleeping bud.<br><br>The tree answers<br>in flowers.</span>';box.appendChild(text);
  }else if(cfg.key==='audio'||cfg.key==='video'){
   media=document.createElement(cfg.key);media.src=cfg.media;media.preload='metadata';media.setAttribute('aria-label',cfg.key==='audio'?'Small hours audio sample':'Passing light video sample');
   if(cfg.key==='video'){media.className='ct-preview-video';media.poster=cfg.poster;media.playsInline=true;media.controls=true;media.hidden=true;media.addEventListener('click',e=>e.stopPropagation());}else media.style.display='none';
   const play=button('Play '+cfg.key,'play',async()=>{if(media.paused){try{if(cfg.key==='video')media.hidden=false;await media.play()}catch{play.title='Try playing again';}}else media.pause()});
   const state=()=>{const name=(media.paused?'Play ':'Pause ')+cfg.key;play.title=name;play.setAttribute('aria-label',name);setIcon(play,media.paused?'play':'pause');draw()};
   media.addEventListener('play',()=>{document.querySelectorAll('audio,video').forEach(other=>{if(other!==media)other.pause()});state()});media.addEventListener('pause',state);media.addEventListener('ended',state);media.addEventListener('timeupdate',draw);box.appendChild(media);
  }
 }
 const resize=()=>{const d=Math.min(devicePixelRatio||1,2);w=el.clientWidth;h=el.clientHeight;canvas.width=w*d;canvas.height=h*d;ctx.setTransform(d,0,0,d,0,0);draw()};const ro=new ResizeObserver(resize);ro.observe(el);resize();
 const io=new IntersectionObserver(es=>{visible=es[0].isIntersecting});io.observe(el);
 const animate=t=>{if(!el.isConnected){ro.disconnect();io.disconnect();if(media)media.pause();return}if(running&&visible&&!document.hidden&&t-last>48){phase+=.025;draw();last=t}requestAnimationFrame(animate)};requestAnimationFrame(animate);
}

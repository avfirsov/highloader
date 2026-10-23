                                                                                                
                   
                                                                                               
                                                                                                    
                                                                                    
                                                          
   
                            
                                                                                           
                                                                                  
import {afterEach,expect,it,vi} from 'vitest';
import {act,useState} from 'react';
import {createRoot} from 'react-dom/client';
import {LiveOwner,mergeReportPage} from './ownership';
import {terminal} from './state';
import {Timeline} from './Timeline';
import {Preparation} from './Preparation';
import {StatusRanges,parseStatuses} from './StatusRanges';
import {Headers} from './Editors';
import {maskURL} from './privacy';
import {App} from './App';
import {Client} from './api';
import type {Draft,RunReport,View} from './types';
const view=(runId='one',revision='1'):View=>({runId,revision,state:'running',workersReleased:false,durable:true,persistenceError:''} as View);
const deferred=<T,>()=>{let resolve!:(value:T)=>void;const promise=new Promise<T>(r=>{resolve=r;});return {promise,resolve};};
const draft:Draft={schemaVersion:1,revision:'1',templates:[{id:'t',filename:'one.json',digest:'digest',raw:btoa('{"Authorization":"source-secret","n":9007199254740993123}'),headers:[{name:'Authorization',value:'file-secret'}],valid:true,diagnostics:[],importOrder:0}],selectedIds:['t'],settings:{t:{headers:[],policy:{},weight:1}},url:'http://localhost?key=url-secret',method:'POST',auth:{mode:'api_key',name:'Custom-Key',value:'custom-secret',placement:'header',token:'',username:'',password:''},headers:[],policy:{timeoutMs:1000,maxRetries:0,statuses:[],network:false,timeouts:false,delayMode:'fixed',delayMs:0,maxDelayMs:0,jitter:false,deadlineMs:1000},profile:{mode:'open',steps:[{level:100,durationSeconds:60}],retryAccounting:'within'},selectionMode:'round_robin',capacity:10,samples:{success:0,failure:0,bodyCapBytes:1024}};
afterEach(()=>vi.unstubAllGlobals());
                                                                                             
                                                                  
                                                                            
                
   
it('rejects an old poll completed after a new start',async()=>{const owner=new LiveOwner();owner.reconnect('s',view());const ticket=owner.ticket(),reply=deferred<View>();const handled=reply.promise.then(v=>owner.accept(ticket,v));owner.start(view('two'));reply.resolve(view('one','99'));expect(await handled).toBe(false);expect(owner.view?.runId).toBe('two');console.info('Stale old-run poll rejected');});
it('rejects an older stop response after a newer poll',async()=>{const owner=new LiveOwner();owner.reconnect('s',view());const ticket=owner.ticket(),reply=deferred<View>();const handled=reply.promise.then(v=>owner.accept(ticket,v));expect(owner.accept(owner.ticket(),{...view('one','9007199254740994'),state:'draining'})).toBe(true);reply.resolve({...view('one','9007199254740993'),state:'stopping'});expect(await handled).toBe(false);expect(owner.view?.state).toBe('draining');});
it('invalidates previous session responses and keeps unsaved final state locked',()=>{const owner=new LiveOwner();owner.reconnect('first',view());const ticket=owner.ticket();owner.reconnect('second',view('new'));expect(owner.accept(ticket,view('one','999'))).toBe(false);expect(terminal({...view(),state:'application_failed',workersReleased:true,durable:false})).toBe(false);expect(terminal({...view(),state:'completed',workersReleased:true,durable:true})).toBe(true);});
it('replaces active report pages from a different cut instead of mixing evidence',()=>{const first={id:'r',cut:'first',population:'all',templateId:'t',points:[{index:'1'}],range:{}} as RunReport;const next={...first,cut:'second',points:[{index:'2'}]} as RunReport;expect(mergeReportPage(first,next)).toEqual({report:next,replaced:true});const same=mergeReportPage(first,{...next,cut:'first'});expect(same.replaced).toBe(false);expect(same.report.points.map(p=>p.index)).toEqual(['1','2']);});
                       
                                                                                        
                                                                      
                                                                          
                
   
it('blocks keyboard and pointer edits on a locked timeline',async()=>{vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);const host=document.createElement('div'),root=createRoot(host),change=vi.fn();await act(async()=>root.render(<Timeline disabled profile={draft.profile} onChange={change}/>));const circle=host.querySelector('circle')!;await act(async()=>{circle.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true}));circle.dispatchEvent(new Event('pointerdown',{bubbles:true}));circle.dispatchEvent(new Event('pointermove',{bubbles:true}));});expect(change).not.toHaveBeenCalled();expect(circle.getAttribute('tabindex')).toBe('-1');expect(circle.getAttribute('aria-disabled')).toBe('true');await act(async()=>root.unmount());});
it('starts closed mode at ten users and restores the prior open profile',async()=>{vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);const host=document.createElement('div'),root=createRoot(host);const changes:Draft[]=[];function Harness(){const [d,setD]=useState(draft);return <Preparation draft={d} onChange={v=>{changes.push(v);setD(v);}} preflight={null} onImport={()=>{}} onValidate={()=>{}} onStart={()=>{}}/>;}await act(async()=>root.render(<Harness/>));const model=Array.from(host.querySelectorAll('select')).find(s=>s.value==='open')!;await act(async()=>{model.value='closed';model.dispatchEvent(new Event('change',{bubbles:true}));});expect(changes.at(-1)?.profile.steps).toEqual([{level:10,durationSeconds:60}]);await act(async()=>{model.value='open';model.dispatchEvent(new Event('change',{bubbles:true}));});expect(changes.at(-1)?.profile.steps).toEqual(draft.profile.steps);await act(async()=>root.unmount());});
it('conceals source and file credentials until explicitly revealed',async()=>{vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);const host=document.createElement('div'),root=createRoot(host);await act(async()=>root.render(<Preparation draft={draft} onChange={()=>{}} preflight={null} onImport={()=>{}} onValidate={()=>{}} onStart={()=>{}}/>));await act(async()=>Array.from(host.querySelectorAll('button')).find(b=>b.textContent==='one.json')!.click());expect(host.textContent).not.toContain('source-secret');expect(host.textContent).not.toContain('file-secret');const reveal=Array.from(host.querySelectorAll('label')).find(l=>l.textContent?.includes('Показать URL'))!.querySelector('input')!;await act(async()=>reveal.click());expect(host.textContent).toContain('source-secret');expect(host.textContent).toContain('file-secret');await act(async()=>root.unmount());});
it('masks configured API key header and URL credentials without changing values',async()=>{vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);const host=document.createElement('div'),root=createRoot(host);await act(async()=>root.render(<Headers apiKey="Custom-Key" rows={[{enabled:true,name:'Custom-Key',value:'secret',suppress:false}]} onChange={()=>{}}/>));const input=host.querySelector('input[aria-label="Значение заголовка 1"]') as HTMLInputElement;expect(input.type).toBe('password');expect(input.value).toBe('secret');expect(maskURL('http://user:password@localhost/path?key=secret')).not.toContain('secret');expect(maskURL('http://user:password@localhost/path?key=secret')).not.toContain('password');await act(async()=>root.unmount());});
it('retains incomplete status text and rejects it only on commit',async()=>{vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);const host=document.createElement('div'),root=createRoot(host),change=vi.fn();await act(async()=>root.render(<StatusRanges disabled={false} value={[]} onChange={change}/>));const input=host.querySelector('input')!;const setter=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')!.set!;await act(async()=>{setter.call(input,'500-');input.dispatchEvent(new Event('input',{bubbles:true}));});expect(change).not.toHaveBeenCalled();expect(input.value).toBe('500-');await act(async()=>input.dispatchEvent(new FocusEvent('focusout',{bubbles:true})));expect(change).not.toHaveBeenCalled();expect(host.querySelector('[role="alert"]')).not.toBeNull();expect(parseStatuses('429,500-599')).toEqual([{from:429,to:429},{from:500,to:599}]);await act(async()=>root.unmount());});
                          
                                                                                        
                                                                                  
                                                                                      
                
   
it('retains the same start key and revision after lost responses',async()=>{
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);const posts:unknown[]=[];
 const transport=vi.fn(async(url:RequestInfo|URL,init?:RequestInit)=>{const path=String(url);if(path.endsWith('/session'))return new Response(JSON.stringify({epoch:'s',csrfToken:'c'}));if(path.endsWith('/active'))return new Response(JSON.stringify({runId:null,view:null}));if(path.includes('/history'))return new Response(JSON.stringify({items:[],nextOffset:null}));if(path.endsWith('/draft/validate'))return new Response(JSON.stringify({effective:[],resources:{},diagnostics:[]}));if(path.endsWith('/draft'))return new Response(JSON.stringify({...draft,revision:init?.method==='PUT'?'2':'1'}));if(path.endsWith('/runs')){posts.push(JSON.parse(String(init?.body)));throw new Error('lost response');}throw new Error(`Unexpected ${path}`);});
 const host=document.createElement('div'),root=createRoot(host);await act(async()=>root.render(<App client={new Client(transport as typeof fetch)}/>));
 await act(async()=>Array.from(host.querySelectorAll('button')).find(b=>b.textContent==='Проверить и запустить')!.click());expect(posts).toHaveLength(1);expect(host.querySelector('fieldset')?.disabled).toBe(true);
 const circle=host.querySelector('circle')!;expect(circle.getAttribute('aria-disabled')).toBe('true');await act(async()=>circle.dispatchEvent(new KeyboardEvent('keydown',{key:'ArrowUp',bubbles:true})));
 await act(async()=>Array.from(host.querySelectorAll('button')).find(b=>b.textContent==='Повторить запрос запуска')!.click());expect(posts).toHaveLength(2);expect(posts[1]).toEqual(posts[0]);expect((posts[0] as {expectedRevision:string}).expectedRevision).toBe('2');
 await act(async()=>root.unmount());console.info('Retry preserves key and freezes settings');
});
                           
                                                                                            
                                                                                           
                                                                                        
                
   
it('renders actual compact history counts and leaves unavailable errors absent',async()=>{
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);
 const items=[{runId:'measured',createdAt:'2026-10-23T12:00:00Z',state:'completed',reason:'',counts:{initial:'9007199254740993',attempts:'9007199254740995',errors:'2'}},{runId:'unknown',createdAt:'2026-10-23T12:00:00Z',state:'interrupted',reason:'',counts:{initial:'3',attempts:'4',errors:null}}];
 const transport=vi.fn(async(input:RequestInfo|URL)=>{const path=String(input);if(path.endsWith('/session'))return new Response(JSON.stringify({epoch:'s',csrfToken:'c'}));if(path.endsWith('/active'))return new Response(JSON.stringify({runId:null,view:null}));if(path.includes('/history'))return new Response(JSON.stringify({items,nextOffset:null}));if(path.endsWith('/draft'))return new Response(JSON.stringify(draft));throw new Error(`Unexpected ${path}`);});
 const host=document.createElement('div'),root=createRoot(host);await act(async()=>root.render(<App client={new Client(transport as typeof fetch)}/>));await act(async()=>Array.from(host.querySelectorAll<HTMLButtonElement>('nav button')).find(b=>b.textContent==='История')!.click());
 const rows=Array.from(host.querySelectorAll('main > section tbody tr'));expect(rows).toHaveLength(2);expect(rows[0].textContent).toContain('Первичные попытки: 9007199254740993');expect(rows[0].textContent).toContain('Все попытки: 9007199254740995');expect(rows[0].textContent).toContain('Ошибки попыток: 2');expect(rows[1].textContent).toContain('Ошибки попыток: —');expect(rows[1].textContent).not.toContain('Ошибки попыток: 0');await act(async()=>root.unmount());
});
                               

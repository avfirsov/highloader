                                                                                                 
                   
                                                                                                            
                                                                                               
                                                                                                   
                                                         
                                                                 
   
                            
                                                                           
                                                                                       
import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import {act} from 'react';
import {createRoot,type Root} from 'react-dom/client';
import {App} from './App';
import {Client} from './api';
import type {Draft,RunReport,Summary,View} from './types';
vi.mock('echarts/core',()=>({use:vi.fn(),init:vi.fn(()=>({setOption:vi.fn(),resize:vi.fn(),dispose:vi.fn()}))}));
let host:HTMLDivElement,root:Root;
beforeEach(()=>{vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);vi.stubGlobal('ResizeObserver',class{observe(){}disconnect(){}});host=document.createElement('div');document.body.append(host);root=createRoot(host);});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();vi.useRealTimers();vi.restoreAllMocks();vi.unstubAllGlobals();});

                                                                                         
                                                                      
                                                                     
                
   
function draft():Draft{return {schemaVersion:1,revision:'9007199254740993',templates:[],selectedIds:[],settings:{},url:'http://localhost',method:'POST',auth:{mode:'none',token:'',username:'',password:'',name:'',value:'',placement:'header'},headers:[],policy:{timeoutMs:1000,maxRetries:0,statuses:[],network:false,timeouts:false,delayMode:'fixed',delayMs:0,maxDelayMs:0,jitter:false,deadlineMs:1000},profile:{mode:'open',steps:[{level:1,durationSeconds:10}],retryAccounting:'within'},selectionMode:'round_robin',capacity:10,samples:{success:0,failure:0,bodyCapBytes:1024}};}
function view():View{return {mode:'open',retryAccounting:'within',runId:'running',revision:'1',state:'running',reason:'',elapsedMs:1000,remainingMs:9000,stage:0,desiredRate:1,initialRate:1,retryRate:0,totalRate:1,shortfall:0,aggregate:{initial:'1',retries:'0',started:'1',statuses:{},attempts:[],logical:[],networkFailures:'0',attemptTimeouts:'0',deadlineInterruptions:'0',bodyReadFailures:'0'},desired:'1',schedulerMisses:'0',capacityMisses:'0',unfinished:1,capacity:10,activeUsers:0,retiringUsers:0,cpu:null,cpuReason:'not available',durableSequence:'1',durableCut:'cut',persistenceError:'',durable:true,workersReleased:false,writerReleased:false,points:[]};}
function report(id='saved'):RunReport{
 const latency={count:'0',minMs:null,meanMs:null,maxMs:null,p50Ms:null,p90Ms:null,p95Ms:null,p99Ms:null,resolutionLimited:'0'};
 const summary:Summary={initial:'0',retries:'0',attempts:'0',logicalStarted:'0',logicalSuccess:'0',logicalFailure:'0',logicalDeadline:'0',logicalCanceled:'0',logicalUnknown:'0',httpErrors:'0',technicalErrors:'0',failedAttempts:'0',canceledAttempts:'0',unknownAttempts:'0',schedulerMisses:'0',capacityMisses:'0',desired:'0',statuses:{},attemptLatency:latency,logicalLatency:latency};
 return {schemaVersion:1,id,revision:'1',durableRevision:'1',snapshot:{runId:id,createdAt:'now',appVersion:'test',draft:draft(),effective:[],resources:{}},state:'completed',reason:'',cut:'cut',durableCut:'cut',durable:true,recovery:null,summary,templates:[],stages:[],points:[],samples:[],range:{fromIndex:'0',nextIndex:null,totalBuckets:'0',downsampled:false}};
}
type Handler=(path:string,init:RequestInit)=>Response|Promise<Response>|undefined;
const json=(data:unknown,status=200)=>new Response(JSON.stringify(data),{status});
async function mount(handler:Handler=()=>undefined){
 const transport=vi.fn(async(input:RequestInfo|URL,options?:RequestInit)=>{
  const path=String(input).replace('/api/v1',''),init=options||{};
  const custom=await handler(path,init);if(custom)return custom;
  if(path==='/session')return json({epoch:'session',csrfToken:'csrf'});
  if(path==='/draft')return json(draft());
  if(path==='/active')return json({runId:null,view:null});
  if(path.startsWith('/history'))return json({items:[],nextOffset:null});
  throw new Error(`Unexpected fixture request ${init.method} ${path}`);
 });
 const client=new Client(transport);await act(async()=>root.render(<App client={client}/>));return {transport,client};
}
async function click(label:string){const button=Array.from(host.querySelectorAll('button')).find(b=>b.textContent===label);expect(button,`button ${label}`).toBeDefined();await act(async()=>button!.click());}
                          

                                                                                                
                                                                                   
                                                                    
                
   
it('loads history pages and repeats a saved run as a new editable draft',async()=>{
 const item=(id:string)=>({runId:id,createdAt:'now',state:'completed',reason:'',url:'http://localhost?key=hidden'});
 const {transport}=await mount((path,init)=>{
  if(path==='/history?offset=0')return json({items:[item('first')],nextOffset:1});
  if(path==='/history?offset=1')return json({items:[item('second')],nextOffset:null});
  if(path==='/runs/first/repeat'){expect(JSON.parse(String(init.body))).toEqual({expectedRevision:'9007199254740993'});return json({...draft(),url:'http://repeated.local',revision:'9007199254740994'});}
 });
 await click('История');expect(host.textContent).not.toContain('key=hidden');await click('Ещё');expect(host.querySelectorAll('section tbody tr')).toHaveLength(2);
 await click('Повторить как новый');expect(host.querySelector('nav [aria-current="page"]')?.textContent).toBe('Подготовка');expect(host.querySelector('fieldset')?.disabled).toBe(false);
 expect(Array.from(host.querySelectorAll('input')).some(input=>input.value==='http://repeated.local')).toBe(true);
 expect(transport.mock.calls.filter(([url])=>String(url).endsWith('/runs'))).toHaveLength(0);
});
it('requires confirmation before deleting a run or clearing all history',async()=>{
 let items=[{runId:'saved',createdAt:'now',state:'completed',reason:''}];
 const {transport}=await mount((path,init)=>{
  if(path==='/history'&&init.method==='DELETE'){items=[];return new Response(null,{status:204});}
  if(path==='/runs/saved'&&init.method==='DELETE'){items=[];return new Response(null,{status:204});}
  if(path.startsWith('/history?'))return json({items,nextOffset:null});
 });
 const confirm=vi.spyOn(globalThis,'confirm').mockReturnValue(false);await click('История');await click('Удалить');expect(transport.mock.calls.some(([,init])=>init?.method==='DELETE')).toBe(false);
 await act(async()=>host.querySelector<HTMLInputElement>('section tbody input[type="checkbox"]')!.click());
 confirm.mockReturnValue(true);await click('Удалить');expect(host.textContent).toContain('Сохранённых запусков нет');
 items=[{runId:'other',createdAt:'now',state:'completed',reason:''}];await click('История');await click('Очистить историю');expect(host.textContent).toContain('Сохранённых запусков нет');
 expect(transport.mock.calls.filter(([,init])=>init?.method==='DELETE')).toHaveLength(2);
 console.info('Confirmed deletion refreshes history; cancellation performs no mutation');
});
it('keeps history visible and shows server failure when deletion fails',async()=>{
 await mount((path,init)=>path.startsWith('/history?')?json({items:[{runId:'saved',state:'completed'}],nextOffset:null}):path==='/runs/saved'&&init.method==='DELETE'?json({message:'history is busy'},409):undefined);
 vi.spyOn(globalThis,'confirm').mockReturnValue(true);await click('История');await click('Удалить');expect(host.querySelector('[role="alert"]')?.textContent).toBe('history is busy');expect(host.querySelectorAll('section tbody tr')).toHaveLength(1);
});
                                

                                                                                         
                                                                     
                                                                                             
                
   
it('attaches to a live run, drains, force stops and unlocks only a durable released result',async()=>{
 await mount(path=>{
  if(path==='/active')return json({runId:'running',view:view()});
  if(path==='/runs/running/stop')return json({...view(),revision:'2',state:'draining'});
  if(path==='/runs/running/force-stop')return json({...view(),revision:'3',state:'stopped',workersReleased:true,writerReleased:true});
 });
 expect(host.querySelector('nav [aria-current="page"]')?.textContent).toBe('Текущий тест');expect(host.querySelector('fieldset')?.disabled).toBe(true);
 await click('Остановить');expect(host.textContent).toContain('Новые запросы остановлены');expect(host.querySelector('fieldset')?.disabled).toBe(true);
 await click('Не дожидаться отправленных');expect(host.textContent).toContain('Исполнители освобождены, данные сохранены');expect(host.querySelector('fieldset')?.disabled).toBe(false);
 console.info('Graceful and force stop retain lock until durable worker/writer release');
});
it('exposes unsaved finalization and confirms deliberate shutdown/discard',async()=>{
 const {transport}=await mount(path=>path==='/active'?json({runId:'running',closing:true,view:{...view(),state:'application_failed',workersReleased:true,writerReleased:true,durable:false,persistenceError:'disk full'}}):path==='/exit/discard'?new Response(null,{status:204}):undefined);
 expect(host.textContent).toContain('Ошибка сохранения: disk full');expect(host.querySelector('fieldset')?.disabled).toBe(true);
 const confirm=vi.spyOn(globalThis,'confirm').mockReturnValue(false);await click('Закрыть без сохранения');expect(transport.mock.calls.some(([url])=>String(url).endsWith('/exit/discard'))).toBe(false);
 confirm.mockReturnValue(true);await click('Закрыть без сохранения');expect(transport.mock.calls.some(([url])=>String(url).endsWith('/exit/discard'))).toBe(true);
});
it('reconnects after startup failure and exits only after confirmation',async()=>{
 let failed=true;const {transport}=await mount(path=>{if(path==='/session'&&failed){failed=false;throw new Error('offline');}if(path==='/exit')return new Response(null,{status:204});});
 expect(host.textContent).toContain('offline');await click('Переподключить');expect(host.querySelector('[role="status"]')?.textContent).toBe('Подключено');
 const confirm=vi.spyOn(globalThis,'confirm').mockReturnValue(false);await click('Выйти');expect(transport.mock.calls.some(([url])=>String(url).endsWith('/exit'))).toBe(false);
 confirm.mockReturnValue(true);await click('Выйти');expect(host.querySelector('[role="status"]')?.textContent).toBe('Завершение приложения');expect(host.querySelector('fieldset')?.disabled).toBe(true);
});
                           

                                                                                             
                                                                      
                                                                                                   
                
   
it('reloads a changed report cut, changes population and exports with explicit authorization',async()=>{
 let loads=0;const {client}=await mount(path=>{
  if(path.startsWith('/history?'))return json({items:[{runId:'saved',state:'completed'}],nextOffset:null});
  if(path.includes('/runs/saved/report')){
   if(path.includes('fromIndex='))return json({message:'Срез отчёта изменился'},409);
   loads++;return json({...report(),range:{...report().range,nextIndex:loads===1?'1':null}});
  }
 });
 const download=vi.spyOn(client,'download').mockResolvedValue();await click('История');
 const rowButton=host.querySelector<HTMLButtonElement>('section tbody button')!;await act(async()=>rowButton.click());expect(host.textContent).toContain('Отчёт saved');
 await click('Следующие интервалы');expect(host.textContent).toContain('загружена первая страница новой ревизии');
 const authorization=Array.from(host.querySelectorAll('label')).find(label=>label.textContent?.includes('Включить авторизацию'))!.querySelector('input')!;
 await act(async()=>authorization.click());await click('Скачать автономный HTML');expect(download).toHaveBeenLastCalledWith({runId:'saved',includeAuthorization:true});
 const population=host.querySelector<HTMLSelectElement>('section .toolbar select')!;await act(async()=>{population.value='success';population.dispatchEvent(new Event('change',{bubbles:true}));});
 await click('Скачать автономный HTML');expect(download).toHaveBeenLastCalledWith({runId:'saved',includeAuthorization:false});
 console.info('Revision conflict reloads evidence; changing population resets export disclosure');
});
                         

                                                                                               
                                                                                     
                                                                                                  
                
   
it('preserves canceled imports, accepts new snapshots and refuses start until preflight succeeds',async()=>{
 let canceled=true,invalid=true;
 const {transport}=await mount((path,init)=>{
  if(path==='/import-folder')return json({canceled,draft:{...draft(),url:'http://imported.local'}});
  if(path==='/draft'&&init.method==='PUT')return json({...draft(),revision:'9007199254740994'});
  if(path==='/draft/validate')return json({effective:[],resources:{totalBytes:100,durationMs:10000},diagnostics:invalid?[{field:'url',message:'invalid local target'}]:[]});
  if(path==='/runs')return json({runId:'running',view:view()});
 });
 await click('Выбрать папку JSON / JSONC');expect(Array.from(host.querySelectorAll('input')).some(input=>input.value==='http://imported.local')).toBe(false);
 canceled=false;await click('Выбрать папку JSON / JSONC');expect(Array.from(host.querySelectorAll('input')).some(input=>input.value==='http://imported.local')).toBe(true);
 await click('Сохранить и проверить');expect(host.textContent).toContain('invalid local target');
 await click('Проверить и запустить');expect(host.querySelector('[role="alert"]')?.textContent).toBe('Исправьте ошибки проверки');
 expect(transport.mock.calls.filter(([url])=>String(url).endsWith('/runs'))).toHaveLength(0);expect(host.querySelector('fieldset')?.disabled).toBe(false);
 invalid=false;await click('Проверить и запустить');expect(host.querySelector('nav [aria-current="page"]')?.textContent).toBe('Текущий тест');expect(host.querySelector('fieldset')?.disabled).toBe(true);
 const starts=transport.mock.calls.filter(([url])=>String(url).endsWith('/runs'));expect(starts).toHaveLength(1);expect(JSON.parse(String(starts[0][1]?.body))).toEqual({expectedRevision:'9007199254740994',idempotencyKey:expect.any(String)});
 console.info('Canceled import preserves draft; validation prevents start; valid start freezes settings');
});
                                    

                                                                                        
                                                                           
                                                                                                     
                
   
it('serializes polling, shows disconnect and restores connectivity after the next authoritative response',async()=>{
 vi.useFakeTimers();let polls=0,finish!:(response:Response)=>void;
 const {transport}=await mount(path=>{
  if(path!=='/active')return;
  polls++;if(polls===2)return new Promise<Response>(resolve=>{finish=resolve;});
  if(polls===3)throw new Error('connection gone');return json({runId:null,view:null});
 });
 await act(async()=>{await vi.advanceTimersByTimeAsync(1000);});expect(polls).toBe(2);
 await act(async()=>{await vi.advanceTimersByTimeAsync(5000);});expect(polls).toBe(2);
 await act(async()=>finish(json({runId:null,view:null})));await act(async()=>{await vi.advanceTimersByTimeAsync(1000);});
 expect(host.querySelector('[role="status"]')?.textContent).toBe('Нет соединения');expect(host.querySelector('fieldset')?.disabled).toBe(true);
 await act(async()=>{await vi.advanceTimersByTimeAsync(1000);});expect(host.querySelector('[role="status"]')?.textContent).toBe('Подключено');
 const last=transport.mock.calls.filter(([url])=>String(url).endsWith('/active')).at(-1)![1]?.signal;
 await act(async()=>root.render(<div/>));expect(last?.aborted).toBe(true);
 await vi.advanceTimersByTimeAsync(5000);expect(polls).toBe(4);
 console.info('Pending poll never overlaps; disconnect recovers and unmount aborts ownership');
});
it('reconciles a lost start through polling without submitting another run',async()=>{
 vi.useFakeTimers();let active=false;const {transport}=await mount(path=>{
  if(path==='/draft/validate')return json({effective:[],resources:{},diagnostics:[]});
  if(path==='/runs'){active=true;throw new Error('lost acknowledgement');}
  if(path==='/active')return json(active?{runId:'running',view:view()}:{runId:null,view:null});
 });
 await click('Проверить и запустить');expect(host.textContent).toContain('Ответ запуска не подтверждён');
 await act(async()=>{await vi.advanceTimersByTimeAsync(1000);});expect(host.textContent).not.toContain('Ответ запуска не подтверждён');expect(host.querySelector('fieldset')?.disabled).toBe(true);
 expect(transport.mock.calls.filter(([url])=>String(url).endsWith('/runs'))).toHaveLength(1);
 await click('Текущий тест');expect(host.textContent).toContain('Выполняется · running');
});
                         

                                                                                                      
                                                                                   
                                                                                                    
                
   
it('limits selection to two runs and carries the chosen pair into comparison and export',async()=>{
 const {client,transport}=await mount((path,init)=>{
  if(path.startsWith('/history?'))return json({items:['left','right','third'].map(runId=>({runId,state:'completed'})),nextOffset:null});
  if(path==='/runs/left/report?limit=1')return json(report('left'));
  if(path==='/runs/right/report?limit=1')return json(report('right'));
  if(path==='/compare'){expect(JSON.parse(String(init.body))).toEqual({leftId:'left',rightId:'right',mapping:{}});return json({schemaVersion:1,runs:[report('left'),report('right')],differences:[],deltas:[],alignment:'elapsed'});}
 });
 const download=vi.spyOn(client,'download').mockResolvedValue();await click('История');
 const selections=Array.from(host.querySelectorAll<HTMLInputElement>('section tbody input[type="checkbox"]'));
 await act(async()=>selections[0].click());await act(async()=>selections[0].click());await act(async()=>selections[0].click());await act(async()=>selections[1].click());expect(selections[2].disabled).toBe(true);
 await click('Сопоставить два запуска');expect(host.textContent).toContain('Сопоставление шаблонов');await click('Построить сравнение');expect(host.textContent).toContain('Сравнение запусков');
 expect(host.querySelector<HTMLSelectElement>('section .toolbar select')?.disabled).toBe(true);await click('Скачать автономный HTML');expect(download).toHaveBeenCalledWith({leftId:'left',rightId:'right',mapping:{},includeAuthorization:false});
 expect(transport.mock.calls.filter(([url])=>String(url).endsWith('/compare'))).toHaveLength(1);
});
it('appends sample pages and replaces changed chart revisions without mixing evidence',async()=>{
 const first={...report(),samples:[{RunID:'saved',TemplateID:'t',LogicalID:'1',Sequence:'1',Attempt:1,Method:'POST'}],range:{...report().range,nextIndex:'1',nextSampleOffset:1,totalSamples:'2'}};
 await mount(path=>{
  if(path==='/active')return json({runId:'running',view:view()});
  if(path.includes('/runs/running/report')){
   if(path.includes('sampleOffset='))return json({...first,samples:[{RunID:'saved',TemplateID:'t',LogicalID:'2',Sequence:'2',Attempt:1,Method:'GET'}],range:{...first.range,nextSampleOffset:null}});
   return json(first);
  }
  if(path.includes('/runs/saved/report')&&path.includes('sampleOffset='))return json({...first,samples:[{RunID:'saved',TemplateID:'t',LogicalID:'2',Sequence:'2',Attempt:1,Method:'GET'}],range:{...first.range,nextSampleOffset:null}});
  if(path.includes('/runs/saved/report')&&path.includes('fromIndex='))return json({...report(),revision:'2',cut:'changed'});
 });
 await click('Доступный отчёт');await click('Следующие образцы');
 const reveal=Array.from(host.querySelectorAll('label')).find(label=>label.textContent?.includes('Показать сохранённые настройки'))!.querySelector('input')!;
 await act(async()=>reveal.click());expect(host.textContent).toContain('Образец 1');expect(host.textContent).toContain('Образец 2');
 await click('Следующие интервалы');expect(host.textContent).toContain('Срез изменился: показана новая страница');expect(host.textContent).toContain('срез changed');expect(host.textContent).not.toContain('Образец 2');
});
                                    

                                                                                                               
                                                                                          
                                                                                                    
                
   
it('keeps a newer polled revision when the pending start acknowledgement arrives late',async()=>{
 vi.useFakeTimers();let started=false,finish!:(response:Response)=>void;
 await mount(path=>{
  if(path==='/draft/validate')return json({effective:[],resources:{},diagnostics:[]});
  if(path==='/runs'){started=true;return new Promise<Response>(resolve=>{finish=resolve;});}
  if(path==='/active')return json(started?{runId:'running',view:{...view(),revision:'2',state:'draining'}}:{runId:null,view:null});
 });
 await click('Проверить и запустить');await act(async()=>{await vi.advanceTimersByTimeAsync(1000);});
 await act(async()=>finish(json({runId:'running',view:view()})));
 expect(host.querySelector('main > section h2')?.textContent).toContain('Ожидание отправленных');
 expect(Array.from(host.querySelectorAll('button')).find(b=>b.textContent==='Остановить')?.disabled).toBe(true);
 expect(host.textContent).not.toContain('Ответ запуска не подтверждён');
});
it('serializes rapid import actions and retains the server error without replacing the draft',async()=>{
 let finish!:(response:Response)=>void;const {transport}=await mount(path=>path==='/import-folder'?new Promise<Response>(resolve=>{finish=resolve;}):undefined);
 const button=Array.from(host.querySelectorAll('button')).find(b=>b.textContent==='Выбрать папку JSON / JSONC')!;
 await act(async()=>{button.click();button.click();});expect(transport.mock.calls.filter(([url])=>String(url).endsWith('/import-folder'))).toHaveLength(1);
 await act(async()=>finish(json({message:'folder unavailable'},400)));expect(host.querySelector('[role="alert"]')?.textContent).toBe('folder unavailable');expect(host.querySelector('fieldset')?.disabled).toBe(false);
});
it('requests the selected report scope and surfaces a non-revision pagination failure',async()=>{
 let pages=0;const {transport}=await mount(path=>{
  if(path.startsWith('/history?'))return json({items:[{id:'saved',state:'completed'}],nextOffset:null});
  if(path.includes('/runs/saved/report')){
   if(path.includes('fromIndex=')){pages++;if(pages===2)return json({message:'storage unavailable'},500);return json({...report(),points:[],range:{...report().range,fromIndex:'1',nextIndex:'2'}});}
   const r=report();r.templates=[{kind:'template',templateId:'scope-id',stage:0,label:'scope',summary:r.summary}];return json({...r,templateId:path.includes('templateId=scope-id')?'scope-id':'',range:{...r.range,nextIndex:'1'}});
  }
 });
 await click('История');await act(async()=>host.querySelector<HTMLButtonElement>('section tbody button')!.click());
 const scope=host.querySelector<HTMLSelectElement>('article select')!;await act(async()=>{scope.value='template:scope-id';scope.dispatchEvent(new Event('change',{bubbles:true}));});
 expect(transport.mock.calls.some(([url])=>String(url).includes('templateId=scope-id'))).toBe(true);
 await click('Следующие интервалы');await click('Следующие интервалы');expect(host.querySelector('[role="alert"]')?.textContent).toBe('storage unavailable');expect(host.textContent).toContain('Отчёт saved');
});
                                 

                                                                                                                  
                                                                                              
                                                                                                    
                
   
it.each(['draft','history'])('ignores an obsolete %s response after reconnect',async(resource)=>{
 let count=0,finish!:(response:Response)=>void;
 const pending=resource==='draft'?'/draft':'/history?offset=0';
 await mount(path=>{
  if(path===pending){count++;if(count===1)return new Promise<Response>(resolve=>{finish=resolve;});}
  if(path==='/draft')return json({...draft(),url:'http://current.local'});
  if(path.startsWith('/history?'))return json({items:[{runId:'current-history',state:'completed'}],nextOffset:null});
 });
 await click('Переподключить');
 if(resource==='history')await click('История');
 await act(async()=>finish(resource==='draft'?json({...draft(),url:'http://obsolete.local'}):json({items:[{runId:'obsolete-history',state:'completed'}],nextOffset:null})));
 expect(Array.from(host.querySelectorAll('input')).some(input=>input.value==='http://current.local')).toBe(true);
 expect(Array.from(host.querySelectorAll('input')).some(input=>input.value==='http://obsolete.local')).toBe(false);
 if(resource==='history'){
  expect(host.textContent).toContain('current-history');expect(host.textContent).not.toContain('obsolete-history');
 }
 console.info('Obsolete session response cannot replace current workflow evidence');
});
                                    

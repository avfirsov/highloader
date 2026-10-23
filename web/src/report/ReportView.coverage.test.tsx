                                                                                            
                   
                                                                                                           
                                                                                           
                                                                                            
                                                                    
                                                                              
   
                            
                                                                           
                                                                                                       
import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import {act} from 'react';
import {createRoot,type Root} from 'react-dom/client';
import {Chart,ReportView} from './ReportView';
import type {ChartPoint,Comparison,Draft,RunReport,Summary} from '../types';
const canvas=vi.hoisted(()=>({setOption:vi.fn(),resize:vi.fn(),dispose:vi.fn()}));
vi.mock('echarts/core',()=>({use:vi.fn(),init:vi.fn(()=>canvas)}));
vi.mock('react-dom/client',async importOriginal=>{const actual=await importOriginal<typeof import('react-dom/client')>();return {...actual,createRoot:vi.fn(actual.createRoot)};});
let host:HTMLDivElement,root:Root;
const disconnect=vi.fn();
let resize:()=>void;
beforeEach(()=>{
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);
 vi.stubGlobal('ResizeObserver',class {constructor(callback:()=>void){resize=callback;}observe(){}disconnect(){disconnect();}});
 host=document.createElement('div');document.body.append(host);root=createRoot(host);vi.clearAllMocks();
});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();vi.restoreAllMocks();vi.unstubAllGlobals();});

                                                                                                
                                                          
                                                                                
                
   
function fixture(id='saved'):RunReport{
 const latency={count:'1',minMs:2,meanMs:2,maxMs:2,p50Ms:2,p90Ms:2,p95Ms:2,p99Ms:2,resolutionLimited:'0'};
 const summary:Summary={initial:'1',retries:'2',attempts:'3',logicalStarted:'1',logicalSuccess:'1',logicalFailure:'0',logicalDeadline:'0',logicalCanceled:'0',logicalUnknown:'0',httpErrors:'2',technicalErrors:'0',failedAttempts:'2',canceledAttempts:'0',unknownAttempts:'0',schedulerMisses:'0',capacityMisses:'0',desired:'1',statuses:{'503':'2','200':'1'},attemptLatency:latency,logicalLatency:latency};
 const draft:Draft={schemaVersion:1,revision:'1',templates:[{id:'t',filename:'input.json',raw:btoa('{"credential":"source-only-secret"}'),digest:'digest',headers:[],diagnostics:[],valid:true,importOrder:0}],selectedIds:['t'],settings:{t:{headers:[],policy:{},weight:1}},url:'http://localhost',method:'POST',auth:{mode:'bearer',token:'settings-only-secret',username:'',password:'',name:'',value:'',placement:'header'},headers:[],policy:{timeoutMs:1000,maxRetries:2,statuses:[{from:503,to:503}],network:false,timeouts:false,delayMode:'fixed',delayMs:1,maxDelayMs:1,jitter:false,deadlineMs:5000},profile:{mode:'closed',retryAccounting:'within',steps:[{durationSeconds:10,level:1}]},selectionMode:'round_robin',capacity:1,samples:{success:1,failure:1,bodyCapBytes:1024}};
 const point:ChartPoint={index:'0',startMs:0,endMs:1000,desiredRate:null,initialRate:1,retryRate:2,totalRate:3,shortfall:null,errorCount:'2',p50Ms:2,p90Ms:2,p95Ms:2,p99Ms:2,cpu:null,cpuReason:'collector unavailable'};
 return {schemaVersion:1,id,revision:'2',durableRevision:'1',snapshot:{runId:id,createdAt:'2026-10-23T00:00:00Z',appVersion:'test',draft,effective:[],resources:{}},state:'interrupted',reason:'recovery',cut:'live-cut',durableCut:null,durable:false,recovery:{checkpoint:'last durable'},summary,templates:[{kind:'template',templateId:'t',stage:0,label:'input.json',summary:{...summary,attempts:'7'}}],stages:[{kind:'stage',stage:0,label:'first',summary:{...summary,attempts:'8'}}],points:[point],samples:[],range:{fromIndex:'0',nextIndex:'1',totalBuckets:'3',downsampled:true,volatileAdditionalKeys:'1'}};
}
                         

                                                                                                
                                                                      
                                                                                  
                
   
it('renders checkpoint limitations and switches template and stage summaries without inventing charts',async()=>{
 const report=fixture(),onScope=vi.fn();await act(async()=>root.render(<ReportView report={report} onScope={onScope}/>));
 expect(host.textContent).toContain('Частичный отчёт');expect(host.textContent).toContain('last durable');
 expect(host.textContent).toContain('collector unavailable');expect(host.textContent).toContain('История пользователей для этого среза недоступна');
 expect(host.textContent).toContain('График объединяет интервалы');expect(host.textContent).toContain('График ограничен страницей');
 expect(host.querySelectorAll('[role="img"]')).toHaveLength(5);
 const select=host.querySelector('select')!;
 await act(async()=>{select.value='template:t';select.dispatchEvent(new Event('change',{bubbles:true}));});
 expect(onScope).toHaveBeenLastCalledWith('t');expect(host.querySelectorAll('[role="img"]')).toHaveLength(0);
 expect(host.textContent).toContain('Общие данные не подставляются');expect(host.querySelector('.metrics')?.textContent).toContain('Все попытки7');
 await act(async()=>{select.value='stage:0';select.dispatchEvent(new Event('change',{bubbles:true}));});
 expect(onScope).toHaveBeenCalledTimes(1);expect(host.querySelector('.metrics')?.textContent).toContain('Все попытки8');
 await act(async()=>{select.value='all';select.dispatchEvent(new Event('change',{bubbles:true}));});
 expect(onScope).toHaveBeenLastCalledWith('');expect(host.querySelectorAll('[role="img"]')).toHaveLength(5);
 console.info('Checkpoint limits and missing template/stage charts remain explicit');
});
it('uses embedded template chart scopes and renders null latency as unavailable',async()=>{
 const report=fixture();report.templateId='t';report.durable=true;report.durableCut='saved';report.revision=report.durableRevision;
 report.summary.attemptLatency.minMs=null;report.templates[0].summary.attemptLatency.minMs=null;
 report.chartScopes={t:[{...report.points[0],targetUsers:'1',activeUsers:'1',retiringUsers:'0'}]};
 await act(async()=>root.render(<ReportView report={report}/>));
 expect((host.querySelector('select') as HTMLSelectElement).value).toBe('template:t');
 expect(host.textContent).toContain('Сохранённый отчёт');expect(host.textContent).not.toContain('История пользователей для этого среза недоступна');
 expect(host.querySelector('tbody tr')?.textContent).toContain('—');
 expect(host.querySelectorAll('[role="img"]')).toHaveLength(5);
});
                                

                                                                                              
                                                                             
                                                                                          
                
   
it('reveals exact sample bodies, absent/binary/truncated states and resets on scope or report changes',async()=>{
 const report=fixture();report.samples=[
  {Method:'POST',Status:200,Request:{Data:btoa('<script>sample-only-secret</script>'),Present:true,Truncated:true},Response:{Present:false}},
  {Request:{Data:btoa('binary-contents'),Present:true,Binary:true},Response:{Data:btoa('saved response'),Present:true}},
 ];
 await act(async()=>root.render(<ReportView report={report}/>));expect(host.textContent).not.toContain('settings-only-secret');expect(host.textContent).not.toContain('sample-only-secret');
 await act(async()=>host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
 for(const text of ['settings-only-secret','source-only-secret','<script>sample-only-secret</script>','Тело усечено','Тело отсутствует','Двоичное тело','Тело сохранено','saved response'])expect(host.textContent).toContain(text);
 expect(host.querySelector('script')).toBeNull();expect(host.textContent).not.toContain('binary-contents');
 const select=host.querySelector('select')!;await act(async()=>{select.value='stage:0';select.dispatchEvent(new Event('change',{bubbles:true}));});
 expect(host.textContent).not.toContain('source-only-secret');
 await act(async()=>host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());
 await act(async()=>root.render(<ReportView report={fixture('other')}/>));expect(host.textContent).not.toContain('settings-only-secret');
 await act(async()=>host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());expect(host.textContent).toContain('Образцов нет');
 console.info('Explicit reveal preserves exact text and resets on scope or identity changes');
});
                            

                                                                                        
                                                                                      
                                                                               
                
   
it('renders two-run deltas, zero baselines and differences only after reveal',async()=>{
 const comparison:Comparison={schemaVersion:1,runs:[fixture('left'),fixture('right')],alignment:'elapsed',differences:[{path:'auth.token',left:'left-secret',right:'right-secret'}],deltas:[{key:'attempts',before:'0',after:'3',absolute:'3',percent:null},{key:'rate',before:1,after:2,absolute:1,percent:100}]};
 await act(async()=>root.render(<ReportView report={comparison}/>));
 expect(host.textContent).toContain('нулевая или отсутствующая база');expect(host.textContent).toContain('Автоматическая оценка результата не выполняется');
 expect(host.textContent).not.toContain('left-secret');
 await act(async()=>host.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click());expect(host.textContent).toContain('left-secret');expect(host.textContent).toContain('right-secret');
 await act(async()=>root.render(<ReportView report={{...comparison,runs:[fixture('new-left'),fixture('right')]}}/>));expect(host.textContent).not.toContain('left-secret');
});
it('passes gaps, elapsed coordinates and labels to canvas and releases observer/chart on unmount',async()=>{
 const point=fixture().points[0];await act(async()=>root.render(<Chart points={[]} keys={['cpu','totalRate']} title="Comparison" overlay={[{label:'A',points:[point]},{label:'B',points:[{...point,startMs:2000,totalRate:9}]}]}/>));
 const options=canvas.setOption.mock.calls.at(-1)![0];
 expect(options.series.map((s:{name:string;data:unknown})=>[s.name,s.data])).toEqual([['A · CPU',[[0,null]]],['A · Все отправки',[[0,3]]],['B · CPU',[[2000,null]]],['B · Все отправки',[[2000,9]]]]);
 expect(options.series.every((s:{connectNulls:boolean})=>!s.connectNulls)).toBe(true);
 resize();expect(canvas.resize).toHaveBeenCalled();await act(async()=>root.render(<div/>));expect(canvas.dispose).toHaveBeenCalledTimes(1);expect(disconnect).toHaveBeenCalledTimes(1);
});
                            

                                                                                         
                                                                           
                                                                                         
                
   
it.each(['not-json','{"schemaVersion":2}',''])('shows a readable offline startup error for %s',async(payload)=>{
 host.id='root';const data=document.createElement('script');data.id='highloader-report-data';data.type='application/json';data.textContent=payload;document.body.append(data);
 vi.resetModules();await import('./offline');expect(host.textContent).toBe('Не удалось прочитать встроенный отчёт.');data.remove();
});
it('leaves an unrelated document intact when the offline mount point is absent',async()=>{
 host.textContent='unrelated document';vi.resetModules();await import('./offline');expect(host.textContent).toBe('unrelated document');
});
it('mounts a complete standalone offline report without fetching anything',async()=>{
 await act(async()=>root.unmount());host.id='root';const data=document.createElement('script');data.id='highloader-report-data';data.type='application/json';data.textContent=JSON.stringify(fixture());document.body.append(data);
 const fetch=vi.fn();vi.stubGlobal('fetch',fetch);vi.resetModules();
 // Capture only the created root for cleanup while preserving the real renderer and report.
 await act(async()=>{await import('./offline');});root=vi.mocked(createRoot).mock.results.at(-1)!.value as Root;
 expect(host.textContent).toContain('Highloader · автономный отчёт');expect(host.textContent).toContain('Отчёт saved');expect(fetch).not.toHaveBeenCalled();
 data.remove();console.info('Embedded report renders through real components without network');
});
                         

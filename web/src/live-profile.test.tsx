                                                                                          
                   
                                                                                    
                                                                                        
                                                                                            
                                          
   
                            
                                                                                 
                                                                               
import {afterEach,expect,it,vi} from 'vitest';
import {act} from 'react';
import {createRoot} from 'react-dom/client';
import {App} from './App';
import {Client} from './api';
import type {ChartPoint,Draft,Profile,View} from './types';
vi.mock('./report/ReportView',()=>({ReportView:()=>null,Chart:({points,title,unit}:{points:ChartPoint[];title:string;unit?:string})=><section data-chart={title} data-unit={unit}><pre>{JSON.stringify(points)}</pre></section>}));
afterEach(()=>vi.unstubAllGlobals());
const makeDraft=(profile:Profile):Draft=>({schemaVersion:1,revision:'1',templates:[],selectedIds:[],settings:{},url:'http://localhost',method:'POST',auth:{mode:'none',name:'',value:'',placement:'header',token:'',username:'',password:''},headers:[],policy:{timeoutMs:1000,maxRetries:0,statuses:[],network:false,timeouts:false,delayMode:'fixed',delayMs:0,maxDelayMs:0,jitter:false,deadlineMs:1000},profile,selectionMode:'round_robin',capacity:10,samples:{success:0,failure:0,bodyCapBytes:1024}});

                                                                                                   
                                                                             
                                                                                               
                
   
it.each([{mode:'closed',accounting:'within',nextMode:'open',nextAccounting:'above'},{mode:'open',accounting:'above',nextMode:'closed',nextAccounting:'within'},{mode:'open',accounting:'within',nextMode:'closed',nextAccounting:'above'}] as const)('retains $mode/$accounting charts after edits, Repeat and reconnect',async({mode,accounting,nextMode,nextAccounting})=>{
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);
 let draft=makeDraft({mode,retryAccounting:accounting,steps:[{level:100,durationSeconds:60}]});
 const current={runId:'old',revision:'9',state:'completed',reason:'',durable:true,workersReleased:true,writerReleased:true,persistenceError:'',mode,retryAccounting:accounting,elapsedMs:1000,remainingMs:0,stage:0,aggregate:{initial:'80',retries:'20'},points:[{Index:0,Start:0,End:1e9,Desired:100,Initial:80,Retries:20,TotalRate:100,TargetUsers:10,ActiveUsers:9,RetiringUsers:1}]} as View;
 const transport=vi.fn(async(input:RequestInfo|URL)=>{const path=String(input);if(path.endsWith('/session'))return new Response(JSON.stringify({epoch:'s',csrfToken:'c'}));if(path.endsWith('/active'))return new Response(JSON.stringify({runId:'old',view:current}));if(path.endsWith('/repeat')){draft=makeDraft({mode:nextMode,retryAccounting:nextAccounting,steps:[{level:10,durationSeconds:60}]});return new Response(JSON.stringify(draft));}if(path.includes('/history'))return new Response(JSON.stringify({items:[{runId:'other',createdAt:'2026-10-23',state:'completed',reason:''}],nextOffset:null}));if(path.endsWith('/draft'))return new Response(JSON.stringify(draft));throw new Error(`Unexpected ${path}`);});
 const host=document.createElement('div'),root=createRoot(host);
 const click=async(text:string)=>{await act(async()=>Array.from(host.querySelectorAll('button')).find(b=>b.textContent===text)!.click());};
 const assertCharts=()=>{const load=host.querySelector('[data-chart="Нагрузка в реальном времени"]')!;const p=JSON.parse(load.querySelector('pre')!.textContent!)[0];expect(p.desiredRate).toBe(mode==='open'?100:null);expect(p.shortfall).toBe(mode==='open'?(accounting==='above'?20:0):null);expect(p.initialRate).toBe(80);expect(p.retryRate).toBe(20);const users=host.querySelector('[data-chart="Виртуальные пользователи"]');if(mode==='closed'){expect(users?.getAttribute('data-unit')).toBe('пользователей');expect(JSON.parse(users!.querySelector('pre')!.textContent!)[0]).toMatchObject({targetUsers:'10',activeUsers:'9',retiringUsers:'1'});}else expect(users).toBeNull();};
 try {
  await act(async()=>root.render(<App client={new Client(transport as typeof fetch)}/>));
  await click('Текущий тест');assertCharts();await click('Подготовка');expect(host.querySelector('fieldset')?.disabled).toBe(false);
  // Change accounting independently before switching modes: 100 desired - 80 initial differs from total 100.
  if(mode==='open'){const accountingSelect=Array.from(host.querySelectorAll('select')).find(s=>s.value===accounting)!;await act(async()=>{accountingSelect.value=accounting==='above'?'within':'above';accountingSelect.dispatchEvent(new Event('change',{bubbles:true}));});await click('Текущий тест');assertCharts();await click('Подготовка');}
  const model=Array.from(host.querySelectorAll('select')).find(s=>s.value===mode)!;await act(async()=>{model.value=nextMode;model.dispatchEvent(new Event('change',{bubbles:true}));});await click('Текущий тест');assertCharts();
  await click('История');await click('Повторить как новый');await click('Текущий тест');assertCharts();
  await click('Переподключить');await click('Текущий тест');assertCharts();
  console.info(`${mode}/${accounting}: edit, Repeat and reconnect retain chart semantics`);
 } finally {await act(async()=>root.unmount());}
});
                                  

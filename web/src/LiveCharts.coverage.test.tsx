                                                                                          
                   
                                                                                            
                                                                                        
                                                                                             
                                                              
                                                                
   
                            
                                                                                       
                                                                                             
import {afterEach,beforeEach,expect,it,vi} from 'vitest';
import {act} from 'react';
import {createRoot,type Root} from 'react-dom/client';
import {LiveCharts} from './LiveCharts';
const canvas=vi.hoisted(()=>({setOption:vi.fn(),resize:vi.fn(),dispose:vi.fn()}));
vi.mock('echarts/core',()=>({use:vi.fn(),init:vi.fn(()=>canvas)}));
let host:HTMLDivElement,root:Root;
beforeEach(()=>{
 vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT',true);
 vi.stubGlobal('ResizeObserver',class {observe(){}disconnect(){}});
 host=document.createElement('div');document.body.append(host);root=createRoot(host);vi.clearAllMocks();
});
afterEach(async()=>{await act(async()=>root.unmount());host.remove();vi.unstubAllGlobals();});
const point={Index:3,Start:1e9,End:1.25e9,Desired:10,Initial:6,Retries:2,TotalRate:32,CompletedRate:28,Errors:2,P50:1500000,P90:2500000,P95:3500000,P99:4500000,CPU:42.5,TargetUsers:8,ActiveUsers:6,RetiringUsers:2};

                                                                                                  
                                                                                             
                                                                                         
                
   
it.each([true,false])('converts partial intervals with above=%s',async above=>{
 await act(async()=>root.render(<LiveCharts points={[point,{...point,Index:4,Start:1.25e9,End:1.5e9,Desired:1}]} open above={above}/>));
 const options=canvas.setOption.mock.calls.map(call=>call[0]);
 expect(options[0].series.map((s:{data:unknown})=>s.data)).toEqual([
  [[1000,40],[1250,4]],[[1000,24],[1250,24]],[[1000,8],[1250,8]],
  [[1000,32],[1250,32]],[[1000,28],[1250,28]],[[1000,above?16:8],[1250,0]],
 ]);
 expect(options[1].series.map((s:{data:unknown})=>s.data)).toEqual([[[1000,1.5],[1250,1.5]],[[1000,3.5],[1250,3.5]],[[1000,4.5],[1250,4.5]]]);
 expect(options[2].series[0].data).toEqual([[1000,42.5],[1250,42.5]]);
 expect(options[3].series[0].data).toEqual([[1000,'2'],[1250,'2']]);
 expect(options.map(o=>o.yAxis.name)).toEqual(['запросов/с','мс','%','ошибок']);
 expect(host.textContent).not.toContain('пока недоступна');
 console.info('Partial duration, retry accounting, latency, CPU and errors verified');
});
                                  

                                                                                                       
                                                                                          
                                                                                        
                
   
it('preserves optional gaps and zero-duration rates without inventing error zeros',async()=>{
 const missing={Index:0,Start:0,End:0,Desired:0,Initial:0,Retries:0};
 await act(async()=>root.render(<LiveCharts points={[missing]} open above/>));
 const options=canvas.setOption.mock.calls.map(call=>call[0]);
 expect(options[0].series.map((s:{data:unknown})=>s.data)).toEqual([[[0,null]],[[0,null]],[[0,null]],[[0,null]],[[0,null]],[[0,0]]]);
 for(const option of options.slice(1))for(const series of option.series){expect(series.data).toEqual([[0,null]]);expect(series.connectNulls).toBe(false);}
 expect(host.textContent).toContain('Живая история ошибок завершения пока недоступна');
 await act(async()=>root.render(<LiveCharts points={[]} open above={false}/>));
 expect(canvas.setOption.mock.calls.slice(-4).every(call=>call[0].series.every((s:{data:unknown[]})=>s.data.length===0))).toBe(true);
 console.info('Missing observations stay null and zero-duration rates stay undefined');
});
                                     

                                                                                           
                                                                               
                                                                                             
                
   
it('renders closed users and distinguishes measured zeros from unavailable history',async()=>{
 await act(async()=>root.render(<LiveCharts points={[{...point,Errors:0,CPU:0,CompletedRate:0}]} open={false} above={false}/>));
 let options=canvas.setOption.mock.calls.map(call=>call[0]);
 expect(options[0].series[0].data).toEqual([[1000,null]]);expect(options[0].series[5].data).toEqual([[1000,null]]);
 expect(options[0].series[4].data).toEqual([[1000,0]]);expect(options[2].series[0].data).toEqual([[1000,0]]);expect(options[3].series[0].data).toEqual([[1000,'0']]);
 expect(options[4].series.map((s:{data:unknown})=>s.data)).toEqual([[[1000,'8']],[[1000,'6']],[[1000,'2']]]);
 expect(host.textContent).not.toContain('пока недоступна');
 canvas.setOption.mockClear();
 await act(async()=>root.render(<LiveCharts points={[{Index:0,Start:0,End:1e9,Initial:0,Retries:0}]} open={false} above={false}/>));
 options=canvas.setOption.mock.calls.map(call=>call[0]);
 expect(options[4].series.map((s:{data:unknown})=>s.data)).toEqual([[[0,null]],[[0,null]],[[0,null]]]);
 expect(host.textContent).toContain('История пользователей пока недоступна');
 console.info('Closed counts and measured zero differ from missing observations');
});
                             

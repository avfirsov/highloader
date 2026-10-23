                                                                                    
                   
                                                                                
                                                                                        
                                                                                            
                        
   
                            
                                                        
                                                      
import type {ChartPoint} from './types';
import {Chart} from './report/ReportView';
                                                                               
                                                   
                                                                               
                
   
export function LiveCharts({points,open,above}:{points:unknown[];open:boolean;above:boolean}){const data:ChartPoint[]=(points||[]).map(raw=>{const p=raw as Record<string,number|string|null>;const seconds=(Number(p.End)-Number(p.Start))/1e9;const rate=(key:string)=>seconds>0?Number(p[key])/seconds:null;const ms=(key:string)=>p[key]==null?null:Number(p[key])/1e6;return {index:String(p.Index),startMs:Number(p.Start)/1e6,endMs:Number(p.End)/1e6,desiredRate:open?rate('Desired'):null,initialRate:rate('Initial'),retryRate:rate('Retries'),totalRate:p.TotalRate==null?null:Number(p.TotalRate),shortfall:open?Math.max(0,(rate('Desired')||0)-(above?(rate('Initial')||0):(Number(p.TotalRate)||0))):null,errorCount:p.Errors==null?null:String(p.Errors),p50Ms:ms('P50'),p90Ms:ms('P90'),p95Ms:ms('P95'),p99Ms:ms('P99'),cpu:p.CPU==null?null:Number(p.CPU),completedRate:p.CompletedRate==null?null:Number(p.CompletedRate),targetUsers:p.TargetUsers==null?null:String(p.TargetUsers),activeUsers:p.ActiveUsers==null?null:String(p.ActiveUsers),retiringUsers:p.RetiringUsers==null?null:String(p.RetiringUsers)};});return <><Chart points={data} title="Нагрузка в реальном времени" keys={['desiredRate','initialRate','retryRate','totalRate','completedRate','shortfall']}/><Chart points={data} title="Задержки" keys={['p50Ms','p95Ms','p99Ms']} unit="мс"/><Chart points={data} title="CPU" keys={['cpu']} unit="%"/><Chart points={data} title="Ошибки за интервал" keys={['errorCount']} unit="ошибок"/>{!data.some(p=>p.errorCount!==null)&&<p>Живая история ошибок завершения пока недоступна; используйте отчёт.</p>}{!open&&<><Chart points={data} title="Виртуальные пользователи" keys={['targetUsers','activeUsers','retiringUsers']} unit="пользователей"/>{!data.some(p=>p.targetUsers!=null)&&<p>История пользователей пока недоступна.</p>}</>}</>;}
                            


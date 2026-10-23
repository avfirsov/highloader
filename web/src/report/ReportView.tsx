                                                                                            
                   
                                                                                            
                                                                                               
                                                                                      
                                                                                                           
                                             
   
                            
                                                                    
                                                                      
import {useEffect,useRef,useState} from 'react';
import * as echarts from 'echarts/core';
import {LineChart} from 'echarts/charts';
import {GridComponent,TooltipComponent,LegendComponent,DataZoomComponent} from 'echarts/components';
import {CanvasRenderer} from 'echarts/renderers';
// Use the ESM declaration entry and register every component used by the shared viewer.
echarts.use([LineChart,GridComponent,TooltipComponent,LegendComponent,DataZoomComponent,CanvasRenderer]);
import type {ChartPoint,Report,RunReport} from '../types';
import {decodeRaw} from '../state';
const stateLabels:Record<string,string>={preparing:'Подготовка',running:'Выполняется',stopping:'Остановка',draining:'Ожидание отправленных',completed:'Завершён',stopped:'Остановлен',interrupted:'Прерван',application_failed:'Ошибка приложения'};
const metricLabels:Record<string,string>={initial:'Первичные отправки',retries:'Повторные отправки',attempts:'Все попытки',logicalStarted:'Логические запросы',logicalSuccess:'Успешные запросы',logicalFailure:'Ошибки запросов',logicalDeadline:'Общий срок истёк',logicalCanceled:'Отменённые запросы',logicalUnknown:'Неизвестные запросы',httpErrors:'HTTP-ошибки',technicalErrors:'Технические ошибки',failedAttempts:'Неуспешные попытки',canceledAttempts:'Отменённые попытки',unknownAttempts:'Неизвестные попытки',schedulerMisses:'Пропуски планировщика',capacityMisses:'Пропуски ёмкости',desired:'Запланированные отправки'};
const chartLabels:Record<string,string>={desiredRate:'Целевая частота',initialRate:'Первичные отправки',retryRate:'Повторы',totalRate:'Все отправки',completedRate:'Все завершения',shortfall:'Недобор',errorCount:'Ошибки',cpu:'CPU',targetUsers:'Целевые пользователи',activeUsers:'Активные пользователи',retiringUsers:'Завершающие пользователи',p50Ms:'p50',p90Ms:'p90',p95Ms:'p95',p99Ms:'p99'};
                                                                              
                                                                 
                                                                
                
   
export function Chart({points,title,keys,unit='запросов/с',overlay}:{points:ChartPoint[];title:string;keys:(keyof ChartPoint)[];unit?:string;overlay?:{label:string;points:ChartPoint[]}[]}){
 const host=useRef<HTMLDivElement>(null);
 // Canvas richText tooltips avoid HTML inline-style markers blocked by the application CSP.
 // ECharts 6 defaults legends to the bottom, overlapping the zoom slider; explicit top reserves the existing grid header.
 useEffect(()=>{if(!host.current)return;const chart=echarts.init(host.current);const sets=overlay||[{label:'',points}];const series=sets.flatMap(set=>keys.map(key=>({name:`${set.label}${set.label?' · ':''}${chartLabels[key]||key}`,type:'line',showSymbol:false,connectNulls:false,data:set.points.map(p=>[p.startMs,p[key]??null])})));chart.setOption({animation:false,tooltip:{trigger:'axis',renderMode:'richText',confine:true},legend:{type:'scroll',top:0,left:'center'},grid:{left:75,right:30,top:45,bottom:65},xAxis:{type:'value',name:'От старта, мс',min:0},yAxis:{type:'value',name:unit,min:0},dataZoom:[{type:'inside'},{type:'slider',bottom:5}],series});const observer=new ResizeObserver(()=>chart.resize());observer.observe(host.current);return()=>{observer.disconnect();chart.dispose();};},[points,keys.join(','),unit,overlay]);
 return <div><h4>{title}</h4><div className="chart" ref={host} role="img" aria-label={`${title}, ${unit}`}/></div>;
}
                       
                                                                                     
                                                                       
                                                                       
                
   
export function SingleReport({report:r,onScope}:{report:RunReport;onScope?:(id:string)=>void}){
 const [reveal,setReveal]=useState(false),[dimension,setDimension]=useState(r.templateId?`template:${r.templateId}`:'all');
 useEffect(()=>{setReveal(false);setDimension(r.templateId?`template:${r.templateId}`:'all');},[r.id,r.templateId]);
 const scope=dimension.startsWith('template:')?dimension.slice(9):'';
 const selected=dimension==='all'?null:dimension.startsWith('stage:')?r.stages.find(d=>String(d.stage)===dimension.slice(6)):r.templates.find(d=>d.templateId===scope);
 const summary=selected?.summary||r.summary;
 const points=dimension.startsWith('stage:')?null:r.chartScopes?.[scope]??((r.templateId||'')===scope?r.points:null);
 const choose=(id:string)=>{setDimension(id);setReveal(false);if(onScope&&!id.startsWith('stage:'))onScope(id==='all'?'':id.slice(9));};
 return <article><h2>Отчёт {r.id}</h2><p>{stateLabels[r.state]||r.state} · {r.reason} · срез {r.cut}</p><p className={r.durable?'note':'warning'}>{r.durable?'Сохранённый отчёт':'Частичный отчёт: срез ещё не подтверждён хранилищем'} · подтверждённый срез: {r.durableCut||'нет'}</p>
 {r.recovery!=null&&<details><summary>Восстановление после прерывания</summary><pre>{JSON.stringify(r.recovery,null,2)}</pre></details>}
 <label>Разрез<select value={dimension} onChange={e=>choose(e.target.value)}><option value="all">Весь запуск</option>{r.templates.map(d=><option key={d.templateId} value={`template:${d.templateId}`}>Шаблон: {d.label}</option>)}{r.stages.map(d=><option key={d.stage} value={`stage:${d.stage}`}>Ступень: {d.label}</option>)}</select></label>
 <p>Популяция задержек: {r.population||"all"}. {r.logicalPopulationNote}</p><p>Показано интервалов: {r.points.length}; сохранено в выбранном разрезе: {r.range.totalBuckets}, начало {r.range.fromIndex}. Дополнительно показано несохранённых интервалов: {r.range.volatileAdditionalKeys||"0"}. {r.range.nextIndex!==null?"График ограничен страницей; агрегаты охватывают весь сохранённый срез.":""}</p>{r.revision!==r.durableRevision&&<p className="warning">Выборка основана на сохранённых ключах; значения и ограниченный несохранённый хвост обновлены из памяти. Новые несохранённые образцы могут отсутствовать на этой странице. Полный доступный срез со всеми образцами можно экспортировать.</p>}<div className="metrics">{Object.entries(summary).filter(([,v])=>typeof v==='string').map(([k,v])=><div key={k}><span>{metricLabels[k]||k}</span><strong>{String(v)}</strong></div>)}</div>
 <table><thead><tr><th>Задержка, мс</th><th>Количество</th><th>min</th><th>mean</th><th>p50</th><th>p90</th><th>p95</th><th>p99</th><th>max</th></tr></thead><tbody>{(['attemptLatency','logicalLatency'] as const).map(k=><tr key={k}><th>{k==='attemptLatency'?'Попытка':'Логический запрос'}</th>{(['count','minMs','meanMs','p50Ms','p90Ms','p95Ms','p99Ms','maxMs'] as const).map(f=><td key={f}>{summary[k][f]??'—'}</td>)}</tr>)}</tbody></table>
 <p>CPU: наблюдений {summary.cpu?.count??"0"}, пропусков {summary.cpu?.missing??"0"}. CPU, %: минимум {summary.cpu?.minPercent??'—'}, среднее {summary.cpu?.meanPercent??'—'}, максимум {summary.cpu?.maxPercent??'—'}. Отменённые и неизвестные исходы учитываются отдельно. Пустые интервалы не заменяются нулями.</p>
 {points&&points.some(p=>p.cpu===null&&p.cpuReason)&&<p>CPU недоступен: {[...new Set(points.filter(p=>p.cpu===null).map(p=>p.cpuReason).filter(Boolean))].join("; ")}</p>}<p>Все завершения включают отменённые и неизвестные попытки; измеренная задержка включает только завершённые измеряемые исходы.</p><details><summary>HTTP-статусы</summary><pre>{JSON.stringify(summary.statuses,null,2)}</pre></details>
 {points?<><Chart points={points} title="Нагрузка и недобор" keys={['desiredRate','initialRate','retryRate','totalRate','completedRate','shortfall']}/><Chart points={points} title="Задержки" keys={['p50Ms','p90Ms','p95Ms','p99Ms']} unit="мс"/><Chart points={points} title="Ошибки за интервал" keys={['errorCount']} unit="ошибок"/><Chart points={points} title="CPU" keys={['cpu']} unit="%"/>{r.snapshot.draft.profile.mode==='closed'&&<><Chart points={points} title="Виртуальные пользователи" keys={['targetUsers','activeUsers','retiringUsers']} unit="пользователей"/>{!points.some(p=>p.targetUsers!=null)&&<p>История пользователей для этого среза недоступна.</p>}</>}</>:<p className="warning">Графики выбранного разреза отсутствуют. Общие данные не подставляются. Для ступеней показаны только сохранённые агрегаты с атрибуцией попыток к ступени отправки.</p>}
 {r.range.downsampled&&<p>График объединяет интервалы; распределения объединены сервером.</p>}
 <label className="inline"><input type="checkbox" checked={reveal} onChange={e=>setReveal(e.target.checked)}/>Показать сохранённые настройки и образцы (могут содержать секреты)</label>
 {reveal&&<><details><summary>Снимок настроек</summary><pre>{JSON.stringify({...r.snapshot,draft:{...r.snapshot.draft,templates:undefined}},null,2)}</pre>{r.snapshot.draft.templates.map(t=><details key={t.id}><summary>{t.filename}</summary><pre>{decodeRaw(t.raw)}</pre></details>)}</details><h3>Сохранённые образцы</h3>{!(r.samples||[]).length&&<p>Образцов нет.</p>}{(r.samples||[]).map((s,i)=><details key={i}><summary>Образец {i+1} · {String(s.Method||'')} · статус {String(s.Status??'—')}</summary><pre>{JSON.stringify(s,null,2)}</pre>{['Request','Response'].map(k=>{const body=s[k] as {Data?:string;Truncated?:boolean;Binary?:boolean;Present?:boolean}|undefined;return body&&<div key={k}><h4>{k}</h4><p>{!body.Present?'Тело отсутствует':body.Binary?'Двоичное тело':body.Truncated?'Тело усечено':'Тело сохранено'}</p>{body.Data&&!body.Binary&&<pre>{decodeRaw(body.Data)}</pre>}</div>;})}</details>)}</>}
 </article>;
}
                              
                                                                                          
                                                                      
                                                                        
                
   
export function ReportView({report,onScope}:{report:Report;onScope?:(id:string)=>void}){
 const [showDifferences,setShowDifferences]=useState(false);const identity='runs' in report?report.runs.map(r=>r.id).join('|'):report.id;
 useEffect(()=>setShowDifferences(false),[identity]);
 if(!('runs' in report))return <SingleReport key={report.id} report={report} onScope={onScope}/>;
 const overlay=report.runs.map(r=>({label:r.id,points:r.points}));
 return <><h2>Сравнение запусков</h2><p>Общая ось — время от старта, мс. Автоматическая оценка результата не выполняется.</p><Chart points={[]} overlay={overlay} title="Сопоставление нагрузки" keys={['totalRate','initialRate','retryRate','completedRate']}/><Chart points={[]} overlay={overlay} title="Сопоставление задержек" keys={['p50Ms','p95Ms','p99Ms']} unit="мс"/><Chart points={[]} overlay={overlay} title="Сопоставление CPU" keys={['cpu']} unit="%"/><Chart points={[]} overlay={overlay} title="Сопоставление ошибок" keys={['errorCount']} unit="ошибок"/>
 <table><thead><tr><th>Показатель</th><th>До</th><th>После</th><th>Разница</th><th>%</th></tr></thead><tbody>{report.deltas.map(d=><tr key={d.key}><th>{d.key}</th><td>{d.before??'—'}</td><td>{d.after??'—'}</td><td>{d.absolute??'—'}</td><td>{d.percent===null?'— (нулевая или отсутствующая база)':d.percent}</td></tr>)}</tbody></table>
 <label className="inline"><input type="checkbox" checked={showDifferences} onChange={e=>setShowDifferences(e.target.checked)}/>Показать различия настроек (могут содержать секреты)</label>{showDifferences&&<details><summary>Различия конфигураций ({report.differences.length})</summary><table><tbody>{report.differences.map((d,i)=><tr key={i}><th>{d.path}</th><td><pre>{JSON.stringify(d.left)}</pre></td><td><pre>{JSON.stringify(d.right)}</pre></td></tr>)}</tbody></table></details>}{report.runs.map(r=><SingleReport key={r.id} report={r}/>)}</>;
}
                            






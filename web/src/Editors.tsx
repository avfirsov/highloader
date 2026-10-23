                                                                                            
                   
                                                                                  
                                                                                      
                                                                              
                                               
   
                            
                                                              
                                                                         
import {useState} from 'react';
import {NumberField} from './NumberField';
import {StatusRanges} from './StatusRanges';
import {secretHeader} from './privacy';
import type {Auth,Header,Policy} from './types';
                                                                                    
                                                         
                                                                
                
   
export function Headers({rows,onChange,apiKey='' }:{rows:Header[];onChange:(v:Header[])=>void;apiKey?:string}){const [reveal,setReveal]=useState(false);return <><label className="inline"><input type="checkbox" checked={reveal} onChange={e=>setReveal(e.target.checked)}/>Показать секреты заголовков</label><table><thead><tr><th>Вкл.</th><th>Заголовок</th><th>Значение</th><th>Удалить из запроса</th><th/></tr></thead><tbody>{(rows||[]).map((r,i)=><tr key={i}><td><input aria-label={`Включить заголовок ${i+1}`} type="checkbox" checked={r.enabled} onChange={e=>onChange(rows.map((h,n)=>n===i?{...h,enabled:e.target.checked}:h))}/></td><td><input aria-label={`Имя заголовка ${i+1}`} value={r.name} onChange={e=>onChange(rows.map((h,n)=>n===i?{...h,name:e.target.value}:h))}/></td><td><input aria-label={`Значение заголовка ${i+1}`} type={!reveal&&secretHeader(r.name,apiKey)?'password':'text'} value={r.value} disabled={r.suppress} onChange={e=>onChange(rows.map((h,n)=>n===i?{...h,value:e.target.value}:h))}/></td><td><input type="checkbox" aria-label={`Подавить заголовок ${i+1}`} checked={r.suppress} onChange={e=>onChange(rows.map((h,n)=>n===i?{...h,suppress:e.target.checked}:h))}/></td><td><button onClick={()=>onChange(rows.filter((_,n)=>i!==n))}>×</button></td></tr>)}</tbody></table><button onClick={()=>onChange([...(rows||[]),{enabled:true,name:'',value:'',suppress:false}])}>Добавить заголовок</button></>;}
                         
export const policyLabels:Record<keyof Policy,string>={timeoutMs:'Таймаут попытки, мс',maxRetries:'Дополнительные попытки',statuses:'HTTP-статусы повтора',network:'Повторять сетевые ошибки',timeouts:'Повторять таймауты',delayMode:'Задержка',delayMs:'Начальная задержка, мс',maxDelayMs:'Максимальная задержка, мс',jitter:'Случайный разброс задержки',deadlineMs:'Общий срок запроса, мс'};
                                                                                         
                                                               
                                                                           
                
   
export function PolicyEditor({value,common,onChange}:{value:Partial<Policy>;common?:Policy;onChange:(v:Partial<Policy>)=>void}){
 const set=(k:keyof Policy,v:unknown)=>onChange({...value,[k]:v});
 return <div className="fields">{(Object.keys(policyLabels) as (keyof Policy)[]).map(k=>{const inherited=common!==undefined&&value[k]===undefined;const v=value[k]??common?.[k];return <div key={k}>{common&&<label className="inline"><input type="checkbox" checked={inherited} onChange={e=>{const next={...value};if(e.target.checked)delete next[k];else Object.assign(next,{[k]:common[k]});onChange(next);}}/>Наследовать</label>}<label>{policyLabels[k]}{k==='statuses'?<StatusRanges disabled={inherited} value={v as Policy['statuses']||[]} onChange={ranges=>set(k,ranges)}/>:k==='delayMode'?<select disabled={inherited} value={String(v)} onChange={e=>set(k,e.target.value)}><option value="fixed">Фиксированная</option><option value="exponential">Экспоненциальная</option></select>:typeof v==='boolean'?<input disabled={inherited} type="checkbox" checked={v} onChange={e=>set(k,e.target.checked)}/>:<NumberField disabled={inherited} value={Number(v??0)} onChange={n=>set(k,n)}/>}</label></div>;})}</div>;
}
                              
                                                                                           
                                                              
                                                                                 
                
   
export function AuthEditor({auth,onChange}:{auth:Auth;onChange:(a:Auth)=>void}){const [reveal,setReveal]=useState(false);const keys=auth.mode==='bearer'?['token']:auth.mode==='basic'?['username','password']:auth.mode==='api_key'?['name','value']:[];const labels:Record<string,string>={token:'Токен',username:'Пользователь',password:'Пароль',name:'Имя ключа',value:'Значение ключа'};return <div className="fields"><label>Авторизация<select value={auth.mode} onChange={e=>onChange({...auth,mode:e.target.value})}><option value="none">Нет</option><option value="bearer">Bearer Token</option><option value="basic">Basic Auth</option><option value="api_key">API Key</option></select></label>{keys.map(key=><label key={key}>{labels[key]}<input type={reveal||key==='name'||key==='username'?'text':'password'} value={auth[key as keyof Auth]} onChange={e=>onChange({...auth,[key]:e.target.value})}/></label>)}{auth.mode==='api_key'&&<label>Размещение<select value={auth.placement} onChange={e=>onChange({...auth,placement:e.target.value})}><option value="header">Заголовок</option><option value="query">Параметр URL</option></select></label>}<label className="inline"><input type="checkbox" checked={reveal} onChange={e=>setReveal(e.target.checked)}/>Показать секреты</label></div>;}
                            


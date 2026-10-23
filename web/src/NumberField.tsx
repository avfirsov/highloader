                                                                                             
                   
                                                                          
                                                                      
                                                                                 
                                                
                                                                                               
                         
   
                            
                                                              
                                                          
import {useEffect,useState} from 'react';
                                                                                       
                                                       
                                                                              
                
   
export function NumberField({value,onChange,min=0,integer=true,disabled=false,label}:{value:number;onChange:(n:number)=>void;min?:number;integer?:boolean;disabled?:boolean;label?:string}){
 const [text,setText]=useState(String(value)),[error,setError]=useState('');
 // A replaced invalid edit must not leave an error on the new value; unchanged values retain intermediate edits.
 useEffect(()=>{setText(String(value));setError('');},[value]);
 const commit=()=>{const n=Number(text);if(!text.trim()||!Number.isFinite(n)||Math.abs(n)>Number.MAX_SAFE_INTEGER||integer&&!Number.isSafeInteger(n)||n<min){setError(`Введите ${integer?'целое ':''}число от ${min} до ${Number.MAX_SAFE_INTEGER}`);return;}setError('');onChange(n);};
 return <><input aria-label={label} aria-invalid={!!error} disabled={disabled} inputMode="decimal" value={text} onChange={e=>setText(e.target.value)} onBlur={commit} onKeyDown={e=>{if(e.key==='Enter'){e.preventDefault();commit();}}}/>{error&&<small role="alert" className="error">{error}</small>}</>;
}
                             

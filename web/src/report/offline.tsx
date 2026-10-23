                                                                                    
                   
                                                                   
                                                   
                                                           
                           
   
                            
                                                      
                                                              
import {createRoot} from 'react-dom/client';
import {ReportView} from './ReportView';
import type {Report} from '../types';
import '../styles.css';
const root=document.getElementById('root');
const data=document.getElementById('highloader-report-data');
if(root){try{const report=JSON.parse(data?.textContent||'') as Report;if(report.schemaVersion!==1)throw new Error('Неподдерживаемая версия отчёта');createRoot(root).render(<main><header><h1>Highloader · автономный отчёт</h1></header><ReportView report={report}/></main>);}catch{root.textContent='Не удалось прочитать встроенный отчёт.';}}

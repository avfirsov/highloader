                                                                                   
                   
                                                  
                                          
                           
   
                            
                                          
                        
import {createRoot} from 'react-dom/client';
import {App} from './App';
import './styles.css';
const root=document.getElementById('root');
if(!root)throw new Error('Не найден корневой элемент интерфейса');
createRoot(root).render(<App/>);

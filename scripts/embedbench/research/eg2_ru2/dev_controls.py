from util2 import *
def controls():
 pairs=[
 ('У сотрудника отпуск начинается в четверг.','Когда работник уходит отдыхать?'),
 ('Участники соревнования собираются у северных ворот.','Где встречаются спортсмены?'),
 ('Компания продаёт велосипеды и ремонтирует самокаты.','Чем занимается фирма?'),
 ('Компаньон оплатил половину поездки.','Кто разделил расходы на путешествие?'),
 ('Ученикам перенесли занятие из-за мороза.','Почему урок не состоялся вовремя?'),
 ('Сигнал светофора запрещает переходить дорогу.','Почему пешеходы должны остановиться?'),
 ('Сидр оставили в холодном погребе.','Где хранится яблочный напиток?'),
 ('Оригами складывают из одного листа бумаги.','Из чего делают бумажную фигурку?'),
 ('Работниками музея подготовлена новая выставка.','Кто организовал новую экспозицию?'),
 ('Памятник перенесли ближе к реке.','Где теперь стоит скульптура?'),
 ('The bicycle shop closes early on Sundays.','When does the bike store close early?'),
 ('A red traffic light tells pedestrians to wait.','What signal stops pedestrians?'),
 ('The museum staff prepared a new exhibition.','Who organized the exhibition?'),
 ('The company repairs scooters and sells bicycles.','What services does the business provide?'),
 ('The employee starts a vacation on Thursday.','When does the worker begin time off?'),
 ('The statue was moved closer to the river.','Where is the statue now?')]
 ds=[];qs=[]
 for i,(t,q) in enumerate(pairs):
  rid=f'dev-control-{i:02}';ds.append(dict(id=rid,text=t));qs.append(dict(id=rid,text=q,target=rid,group=rid,kind='ordinary' if i<10 else 'english',source_kind='dev-control'))
 return ds,qs

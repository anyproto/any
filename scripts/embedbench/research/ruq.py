"""ruq.py <wiki set dir> <out dir>: Russian known-item paraphrase proxy
(the account's worst slice). 30 Russian wiki windows, each with a Russian
paraphrase that avoids the window's distinctive words, and the same
paraphrase in English (cross-language). Target = that one window; the
candidate set is all 1200 wiki windows (docs.jsonl copied)."""
import json, os, sys
src, out = sys.argv[1], sys.argv[2]
os.makedirs(out, exist_ok=True)
Q = [
    ("ru-Москва-163", "сколько игр мирового футбольного первенства приняла столица и на каких аренах", "how many games of the football world championship did the capital host and in which arenas"),
    ("ru-Москва-85", "какую долю жителей столицы составляют русские по сравнению со страной в целом", "what share of the capital's residents are ethnic Russians compared with the country as a whole"),
    ("ru-Фотосинтез-10", "какие микроорганизмы способны усваивать свет без образования кислорода", "which microbes can harvest light without producing oxygen"),
    ("ru-Python-76", "планы сделать глобальную блокировку необязательной и собрать интерпретатор без неё", "plans to make the global lock optional and build the interpreter without it"),
    ("ru-Москва-21", "рекорды жары и мороза в столице за всю историю наблюдений", "all-time heat and frost records in the capital"),
    ("ru-Москва-35", "как возникло отдельное княжество со столицей в Москве и кому его завещали", "how a separate principality centred on Moscow came about and who inherited it"),
    ("ru-Python-13", "почему язык назван в честь комедийной труппы и зачем в примерах еда вместо foo", "why the language is named after a comedy troupe and why examples use food words instead of foo"),
    ("ru-Москва-46", "что означает слово первопрестольная и кто основал старейший университет города", "what the word 'first-throned' means and who founded the city's oldest university"),
    ("ru-Фотосинтез-1", "как в науке о растениях определяют превращение света в химическую энергию и как оценивают его эффективность", "how plant science defines turning light into chemical energy and how its efficiency is measured"),
    ("ru-Python-43", "дорого ли обходятся исключения в этом языке и как они связаны с утиной типизацией", "are exceptions expensive in this language and how do they relate to duck typing"),
    ("ru-Москва-23", "сколько солнечных часов в году и какие необычные погодные явления бывают в городе", "how many sunshine hours per year and which unusual weather events occur in the city"),
    ("ru-Фотосинтез-59", "морское животное, которое живёт за счёт захваченных органелл водорослей", "a sea animal that lives off captured algal organelles"),
    ("ru-Москва-113", "кольцевые автодороги столицы и трасса для разгрузки транзита в области", "the capital's ring roads and the bypass built to divert transit traffic in the region"),
    ("ru-Москва-15", "какие притоки и сколько прудов и озёр есть в черте города", "which tributaries and how many ponds and lakes are within the city limits"),
    ("ru-Москва-43", "начало правления новой царской династии и перестройка Кремля в семнадцатом веке", "the start of a new royal dynasty and the rebuilding of the Kremlin in the seventeenth century"),
    ("ru-Фотосинтез-26", "два уровня возбуждения пигмента и синглетные или триплетные состояния", "the pigment's two excitation levels and singlet versus triplet states"),
    ("ru-Фотосинтез-19", "испарение через устьица как двигатель транспорта и образование крахмальных зёрен", "evaporation through stomata driving transport and the formation of starch grains"),
    ("ru-Москва-29", "как ветры влияют на экологию города и сколько станций следят за воздухом", "how winds affect the city's ecology and how many stations monitor the air"),
    ("ru-Москва-123", "введение платной стоянки в центре и прогрессивный тариф", "introduction of paid parking downtown and a progressive rate"),
    ("ru-Москва-45", "перенос столицы на берега Невы и сохранение коронаций в Кремле", "moving the capital to the Neva and keeping coronations in the Kremlin"),
    ("ru-Python-21", "выделение блоков пробелами вместо скобок, плюсы и минусы", "marking code blocks with whitespace instead of braces, pros and cons"),
    ("ru-Фотосинтез-23", "почему органеллы считают потомками бактерий, поселившихся в клетке", "why the organelles are considered descendants of bacteria that settled in the cell"),
    ("ru-Москва-26", "редкие зверьки, обитающие в городских лесах", "rare small animals living in the city's forests"),
    ("ru-Python-33", "возможности объектной модели: специальные методы, статические методы, интроспекция", "object-model features: special methods, static methods, introspection"),
    ("ru-Москва-64", "кто управляет городом, сколько депутатов в думе и когда вернули прямые выборы мэра", "who governs the city, how many deputies are in the assembly and when direct mayoral elections returned"),
    ("ru-Москва-119", "нехватка мест для машин и отмена платы за стоянку у обочин", "shortage of car spaces and the abolition of fees for curbside parking"),
    ("ru-Python-84", "позднее связывание имён и поиск атрибутов через словари", "late binding of names and attribute lookup through dictionaries"),
    ("ru-Python-54", "советы по ускорению программ: сначала алгоритм, потом профилирование", "advice on speeding up programs: algorithm first, then profiling"),
    ("ru-Python-41", "где хранится информация для самоанализа объектов и какой модуль её извлекает", "where object self-inspection data is kept and which module extracts it"),
    ("ru-Python-47", "конструкция, гарантирующая освобождение ресурсов после блока кода", "the construct that guarantees resources are released after a block of code"),
]
docs = [json.loads(l) for l in open(f"{src}/docs.jsonl")]
ids = {d["id"] for d in docs}
qs, qrels = [], {}
for i, (t, ru, en) in enumerate(Q):
    assert t in ids, t
    for kind, text in (("ru-para", ru), ("en-cross", en)):
        qid = f"{kind}-{i}"
        qs.append({"id": qid, "text": text, "kind": kind, "target": t})
        qrels[qid] = [t]
os.system(f"cp {src}/docs.jsonl {out}/")
with open(f"{out}/queries.jsonl", "w") as f:
    for q in qs:
        f.write(json.dumps(q, ensure_ascii=False) + "\n")
json.dump(qrels, open(f"{out}/qrels.json", "w"))
print(len(qs), "queries")

"""wikiq.py <wiki set dir>: writes queries.jsonl + qrels.json for the window
proxy. Each query targets one section of one article; relevant = that
section's windows (path contains the heading) that carry text beyond the
heading. Heading-only windows are never relevant (a bare heading answers
nothing, which is how the account's judge treated them)."""
import json, sys
d = sys.argv[1]
docs = [json.loads(l) for l in open(f"{d}/docs.jsonl")]
Q = [
    # en London
    ("what did the Romans build in London and when was Londinium founded", "en", "London", "Roman London"),
    ("how London's climate compares, rainfall and temperature", "en", "London", "Climate"),
    ("airports serving London and passenger numbers", "en", "London", "Aviation"),
    ("universities and colleges in London", "en", "London", "Tertiary education"),
    ("origin of the name London", "en", "London", "Etymology"),
    ("London parks and green open spaces", "en", "London", "Parks and open spaces"),
    ("which religions do Londoners follow", "en", "London", "Religion"),
    ("the City of London as a financial centre", "en", "London", "Finance"),
    # en Photosynthesis
    ("how water is split to release oxygen in plants", "en", "Photosynthesis", "Water photolysis"),
    ("steps of the Calvin cycle fixing carbon dioxide", "en", "Photosynthesis", "Calvin cycle"),
    ("how efficient is photosynthesis at converting sunlight", "en", "Photosynthesis", "Efficiency"),
    ("origin of chloroplasts from cyanobacteria", "en", "Photosynthesis", "Symbiosis and the origin of chloroplasts"),
    ("who discovered that plants produce oxygen, early experiments", "en", "Photosynthesis", "Discovery"),
    ("effect of light intensity and temperature on photosynthesis rate", "en", "Photosynthesis", "Light intensity (irradiance), wavelength and temperature"),
    # en Python
    ("why python uses indentation for blocks", "en", "Python (programming language)", "Indentation"),
    ("python history, who created it and when", "en", "Python (programming language)", "History"),
    ("PEP process for proposing language changes", "en", "Python (programming language)", "Enhancement Proposals"),
    ("alternative python interpreters like PyPy and Jython", "en", "Python (programming language)", "Other implementations"),
    ("how fast is python compared to other languages", "en", "Python (programming language)", "Performance"),
    ("where does the name Python come from", "en", "Python (programming language)", "Naming"),
    ("dynamic typing and duck typing in python", "en", "Python (programming language)", "Typing"),
    # de Berlin
    ("Wie ist das Klima in Berlin, Niederschlag und Temperaturen", "de", "Berlin", "Klima"),
    ("Berlin im Nationalsozialismus und im Zweiten Weltkrieg", "de", "Berlin", "Nationalsozialismus"),
    ("Welche Sprachen werden in Berlin gesprochen", "de", "Berlin", "Sprachen"),
    ("Berliner Flughäfen und Luftverkehr", "de", "Berlin", "Flugverkehr"),
    ("Universitäten und Hochschulen in Berlin", "de", "Berlin", "Hochschulen"),
    ("Museen in Berlin, Museumsinsel", "de", "Berlin", "Museen"),
    ("Berliner Mauer und die geteilte Stadt", "de", "Berlin", "Geteilte Stadt"),
    ("Seen, Flüsse und Berge im Berliner Stadtgebiet", "de", "Berlin", "Gewässer und Erhebungen"),
    ("Startups und Kreativwirtschaft in Berlin", "de", "Berlin", "IT und Kreativwirtschaft"),
    # de Photosynthese
    ("Wie läuft der Calvin-Zyklus ab", "de", "Photosynthese", "Calvin-Zyklus"),
    ("Wirkungsgrad der Photosynthese bei Pflanzen", "de", "Photosynthese", "Wirkungsgrad"),
    ("Photosynthese ohne Sauerstoffproduktion bei Bakterien", "de", "Photosynthese", "Anoxygene Photosynthese"),
    ("Einfluss der Temperatur auf die Photosyntheserate", "de", "Photosynthese", "Temperatur"),
    ("Geschichte der Erforschung der Photosynthese", "de", "Photosynthese", "Forschungsgeschichte"),
    # de Python
    ("Welche Python-Versionen gibt es und wann erschienen sie", "de", "Python (Programmiersprache)", "Versionen"),
    ("Wie behandelt Python Fehler und Ausnahmen", "de", "Python (Programmiersprache)", "Ausnahmebehandlung"),
    ("Python in Schule und Hochschule als Lehrsprache", "de", "Python (Programmiersprache)", "In der Lehre"),
    ("Kritik an Python, Nachteile der Sprache", "de", "Python (Programmiersprache)", "Kritik"),
    ("Pakete installieren mit pip", "de", "Python (Programmiersprache)", "Paketverwaltung"),
    # ru Moscow
    ("климат Москвы, температура зимой и летом", "ru", "Москва", "Климат"),
    ("московское метро, история и станции", "ru", "Москва", "Метрополитен"),
    ("пробки на дорогах Москвы", "ru", "Москва", "Пробки"),
    ("численность населения Москвы и мигранты", "ru", "Москва", "Численность населения"),
    ("Москва в годы Великой Отечественной войны и советский период", "ru", "Москва", "Советский период"),
    ("Кремль и Красная площадь, архитектура", "ru", "Москва", "Московский Кремль и Красная площадь"),
    ("университеты и высшее образование в Москве", "ru", "Москва", "Высшее"),
    ("религиозный состав жителей Москвы", "ru", "Москва", "Этнический и религиозный состав"),
    ("откуда произошло название Москва", "ru", "Москва", "Этимология"),
    # ru Photosynthesis
    ("как устроена фотосистема II", "ru", "Фотосинтез", "Фотосистема II"),
    ("чем отличается C4-фотосинтез от C3", "ru", "Фотосинтез", "С4-фотосинтез"),
    ("фотосинтез у галофильных архей без хлорофилла", "ru", "Фотосинтез", "Бесхлорофилльный фотосинтез"),
    ("история открытия фотосинтеза", "ru", "Фотосинтез", "История изучения"),
    ("строение хлоропластов", "ru", "Фотосинтез", "Хлоропласты"),
    # ru Python
    ("почему python медленный", "ru", "Python", "Низкое быстродействие"),
    ("что такое GIL в питоне", "ru", "Python", "Глобальная блокировка интерпретатора (GIL)"),
    ("декораторы функций в python", "ru", "Python", "Декораторы"),
    ("генераторы и yield", "ru", "Python", "Генераторы"),
    ("асинхронное программирование asyncio", "ru", "Python", "Асинхронное программирование"),
    ("сравнение python с языком Go", "ru", "Python", "Go"),
    ("история создания языка python", "ru", "Python", "История"),
    # ja
    ("東京の気候、夏の暑さと降水", "ja", "東京都", "気候"),
    ("東京都の人口推移", "ja", "東京都", "東京都人口動態"),
    ("東京の鉄道網", "ja", "東京都", "鉄道"),
    ("戦後の復興と1964年オリンピック", "ja", "東京都", "戦後の復興と東京五輪大会"),
    ("東京都議会の仕組み", "ja", "東京都", "東京都議会"),
    ("光合成の効率はどれくらいか", "ja", "光合成", "光合成の効率"),
    ("光合成はいつどのように進化したか", "ja", "光合成", "光合成の起源"),
    ("pythonのパッケージ管理 pip", "ja", "Python", "パッケージ管理"),
    ("pythonの型ヒント", "ja", "Python", "型ヒント"),
    ("pythonのメモリ管理とガベージコレクション", "ja", "Python", "メモリ管理"),
    # zh
    ("北京的空气污染问题", "zh", "北京市", "空气污染"),
    ("北京地铁", "zh", "北京市", "地铁"),
    ("北京的胡同文化", "zh", "北京市", "胡同"),
    ("明朝时期的北京城", "zh", "北京市", "明代"),
    ("北京的气候特点", "zh", "北京市", "氣候"),
    ("卡尔文循环如何固定二氧化碳", "zh", "光合作用", "卡爾文循環"),
    ("C4植物与C3植物的区别", "zh", "光合作用", "C4类植物"),
    ("python 的缩进规则", "zh", "Python", "縮排"),
    ("python 用于机器学习的库", "zh", "Python", "机器学习"),
    ("python 语言的历史", "zh", "Python", "歷史"),
    # cross-language (query in English, target in another language)
    ("Moscow metro history and stations", "ru", "Москва", "Метрополитен"),
    ("Berlin climate", "de", "Berlin", "Klima"),
    ("Tokyo railway network", "ja", "東京都", "鉄道"),
    ("Beijing air pollution", "zh", "北京市", "空气污染"),
    ("why is Python slow, the GIL", "ru", "Python", "Глобальная блокировка интерпретатора (GIL)"),
]
qs, qrels, drop = [], {}, []
for i, (t, lang, art, sec) in enumerate(Q):
    rel = [x["id"] for x in docs if x["lang"] == lang and x["article"] == art and sec in x["path"] and not x["heading_only"]]
    if not rel:
        drop.append((t, art, sec))
        continue
    qid = f"q{i}"
    qs.append({"id": qid, "text": t, "lang": lang, "article": art, "section": sec})
    qrels[qid] = rel
with open(f"{d}/queries.jsonl", "w") as f:
    for q in qs:
        f.write(json.dumps(q, ensure_ascii=False) + "\n")
json.dump(qrels, open(f"{d}/qrels.json", "w"), ensure_ascii=False)
print(f"{len(qs)} queries, mean relevant {sum(len(v) for v in qrels.values())/len(qrels):.1f}; dropped: {drop}")

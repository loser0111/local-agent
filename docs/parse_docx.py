import zipfile
from xml.etree import ElementTree as ET

NS = '{http://schemas.openxmlformats.org/wordprocessingml/2006/main}'

with zipfile.ZipFile('docs/极兔面试.docx') as z:
    with z.open('word/document.xml') as f:
        tree = ET.parse(f)
        root = tree.getroot()
        for para in root.iter(NS + 'p'):
            texts = []
            for t in para.iter(NS + 't'):
                texts.append(t.text or '')
            line = ''.join(texts)
            print(line)

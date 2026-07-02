#!/usr/bin/env python3
"""One-time export of eliasalbouzidi/distilbert-nsfw-text-classifier
(DistilBERT, Apache-2.0) to ONNX for internal/classify/text. Not part of
the Go build - run occasionally, by hand, whenever the model needs
(re)provisioning. No system Python needed:

    uv run --with transformers --with "optimum[onnxruntime]" --with torch \
        scripts/export_text_model.py --out models/text-nsfw

Produces models/text-nsfw/{model.onnx, vocab.txt, config.json} - see
internal/classify/text's package doc for the exact runtime format those
three files must match.
"""
import argparse
import json
import shutil
from pathlib import Path

from optimum.exporters.onnx import main_export

MODEL_ID = "eliasalbouzidi/distilbert-nsfw-text-classifier"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True, help="output directory")
    ap.add_argument("--model", default=MODEL_ID, help="HuggingFace model id to export")
    args = ap.parse_args()
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)

    print(f"[export] exporting {args.model} to ONNX in {out} ...")
    main_export(model_name_or_path=args.model, output=out, task="text-classification")

    onnx_files = sorted(out.glob("*.onnx"))
    if not onnx_files:
        raise SystemExit(f"[export] no .onnx file found in {out} after export - check optimum's output above")
    onnx_path = onnx_files[0]
    if onnx_path.name != "model.onnx":
        onnx_path.rename(out / "model.onnx")
        print(f"[export] renamed {onnx_path.name} -> model.onnx")

    vocab_src = out / "vocab.txt"
    if not vocab_src.exists():
        raise SystemExit(f"[export] {vocab_src} missing after export - optimum's tokenizer files should include it")

    with open(out / "config.json", encoding="utf-8") as f:
        full_config = json.load(f)
    with open(out / "tokenizer_config.json", encoding="utf-8") as f:
        tok_config = json.load(f)

    id2label = full_config.get("id2label")
    if not id2label:
        raise SystemExit(f"[export] {args.model}'s config.json has no id2label")

    minimal_config = {
        "max_position_embeddings": full_config["max_position_embeddings"],
        "do_lower_case": tok_config.get("do_lower_case", True),
        "id2label": id2label,
        "pad_token_id": full_config.get("pad_token_id", 0),
        "unk_token_id": tok_config.get("unk_token_id_value", 100),
        "cls_token_id": tok_config.get("cls_token_id_value", 101),
        "sep_token_id": tok_config.get("sep_token_id_value", 102),
    }
    # Prefer resolving special-token ids from the tokenizer's own
    # added_tokens_decoder (authoritative), falling back to the standard
    # bert-base-uncased ids above if that structure isn't present.
    added = tok_config.get("added_tokens_decoder", {})
    content_to_id = {v["content"]: int(k) for k, v in added.items()}
    minimal_config["pad_token_id"] = content_to_id.get(tok_config.get("pad_token", "[PAD]"), minimal_config["pad_token_id"])
    minimal_config["unk_token_id"] = content_to_id.get(tok_config.get("unk_token", "[UNK]"), minimal_config["unk_token_id"])
    minimal_config["cls_token_id"] = content_to_id.get(tok_config.get("cls_token", "[CLS]"), minimal_config["cls_token_id"])
    minimal_config["sep_token_id"] = content_to_id.get(tok_config.get("sep_token", "[SEP]"), minimal_config["sep_token_id"])

    with open(out / "config.json", "w", encoding="utf-8") as f:
        json.dump(minimal_config, f, indent=2)

    # Drop everything optimum/transformers wrote that internal/classify/text
    # doesn't read, so the shipped model directory only contains the three
    # files its package doc documents.
    keep = {"model.onnx", "vocab.txt", "config.json"}
    for path in out.iterdir():
        if path.is_file() and path.name not in keep:
            path.unlink()
        elif path.is_dir():
            shutil.rmtree(path)

    print(f"[export] wrote {out}/model.onnx, vocab.txt, config.json")
    print(f"[export] id2label: {id2label}")


if __name__ == "__main__":
    main()

# Surgery ⚡

**Claude Code の会話をコンパクションして、トークンを大幅削減**

長くなった Claude Code セッションの会話履歴（JSONL）を画像化・最適化してトークン消費を削減するツール。テキストより画像の方がトークン効率が良いという知見に基づき、過去の会話をチャット風画像に変換する。

**実測**: 122,721 tokens → 65,237 tokens（**46.8% 削減**）

## インストール

```sh
curl -fsSL https://raw.githubusercontent.com/Mojashi/claude-conversation-editor/master/install.sh | sh
```

`~/bin` に PATH が通っていない場合は `SURGERY_BIN_DIR` で指定:

```sh
curl -fsSL https://raw.githubusercontent.com/Mojashi/claude-conversation-editor/master/install.sh | SURGERY_BIN_DIR=/usr/local/bin sh
```

<details>
<summary>手動インストール</summary>

[Releases](https://github.com/Mojashi/claude-conversation-editor/releases) から最新の zip をダウンロードして展開。

```bash
cp -r Surgery.app /Applications/
ln -sf /Applications/Surgery.app/Contents/MacOS/surgery ~/bin/surgery
```
</details>

## コマンド一覧

| コマンド | 説明 |
|------|------|
| `surgery` | GUI エディタを開く（デフォルト） |
| `surgery view` | GUI エディタを開く |
| `surgery compact` | 会話を画像化してトークン削減 |
| `surgery branch` | 会話の途中から分岐して新セッション作成 |
| `surgery update` | 最新バージョンに更新 |

Claude Code 内では `!surgery compact`、`!surgery branch` のように実行できる。

## Compact

会話履歴をチャット風の画像に変換し、トークンを削減する。最後の assistant + user ターンはテキストのまま残すので、Claude は自然に会話を継続できる。

```bash
# Claude Code のセッション内で（推奨）
!surgery compact

# ターミナルから（最新セッションを自動検出）
surgery compact

# セッション指定 / ドライラン
surgery compact <session-id> --dry-run
surgery compact <session-id> --inplace
```

Claude Code 内から実行すると、現在のセッションを自動検出してコンパクト用ウィンドウを開く。完了後:

- レンダリング結果のプレビュー
- compact レポート（エントリ数・トークン数の変化）
- `/resume` コマンド（コピーボタン付き）

### コンパクションの内容

| 処理 | 説明 |
|------|------|
| Text-to-Image | 会話全体をチャット風 HTML → WebKit レンダリング → PNG/WebP 画像に変換 |
| 画像・PDF の埋め込み | 会話中の画像や PDF ドキュメントをそのまま画像内に埋め込み（画像 in 画像） |
| 冗長な Read 除去 | 同一ファイルの古い Read 結果を切り詰め |
| 冗長な Write 除去 | 同一ファイルへの古い Write を切り詰め |
| 成功メッセージ短縮 | 冗長なツール結果を短縮 |

### オプション: WebP 変換

`cwebp` がインストールされていれば自動的に PNG → WebP 変換してさらにサイズを削減。

```bash
brew install webp
```

## Branch

会話の途中のメッセージを選んで、そこまでの履歴で新しいセッションを作成する。長い会話から分岐して別のアプローチを試したいときに便利。

```bash
# Claude Code のセッション内で
!surgery branch

# ターミナルから（最新セッションを自動検出）
surgery branch

# セッション指定
surgery branch <session-id>
```

ブランチウィンドウでメッセージ一覧が表示されるので、分岐したいメッセージをクリック。新しいセッションが作成され、`/resume` コマンドが表示される。

## エディタ（GUI）

会話を手動で編集する GUI エディタ。

```bash
# Claude Code 内で
!surgery

# ターミナルから
surgery
```

| 操作 | 説明 |
|------|------|
| チェックボックス | 削除対象を選択 |
| Shift + クリック | 範囲選択 |
| ✂ Truncate after | そのメッセージ以降を全選択 |
| Tools / Sidechain | tool_use・サイドチェーンの表示切替 |
| Delete Selected | 選択をプレビュー |
| Save | JSONL に書き込み（`.jsonl.bak` に自動バックアップ） |

## 自動アップデート

起動時に新バージョンがあればヘッダーに通知が出る。クリックで自動ダウンロード＆再起動。

## ビルド

```bash
# 依存: Go 1.21+, Node 18+, Wails v2
go install github.com/wailsapp/wails/v2/cmd/wails@latest
make build
```

## License

MIT

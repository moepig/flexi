# 依存更新とリリースの運用

リポジトリ管理者向けに、依存更新の自動マージと、リリース PR から配布物を公開する手順を示す。認証には各ジョブの GITHUB_TOKEN を使う。セキュリティ修正の根拠を登録する操作はない。

## 導入条件

Actions の設定で「Allow GitHub Actions to create and approve pull requests」を有効にする。main の ruleset には GitHub Actions 提供の ci、dependency-review、release-ready を必須にする。ブランチを main に対して最新にすること、レビュー 1 件以上、push 後の承認無効化も要求する。CODEOWNERS など追加のレビュー条件は維持する。

各リポジトリの .github/dependency-release.json で、必須 CI、依存定義、配布物、互換性の確認対象を確認する。配布物に影響しない依存は noRelease に名前、定義ファイル、用途を記録する。用途が不明な依存はリリース対象として扱う。

旧 auto-merge とタグ起点の Release ワークフローを停止してから新しい経路へ切り替える。flexi のソース公開、dd-conf-gen の添付ファイル公開を先に検証する。その後に他のリポジトリを切り替える。自動マージは必須チェックと保護設定を API で確認できるまで保留される。

zaikokanri は現在のプランではブランチ保護を利用できないため、自動マージを行わず手動で確認する。公開範囲やプランの変更後も、ruleset を設定してから自動マージを有効にする。

## 初期バージョンを確認する

既存リリースのあるリポジトリでは、manifest と bootstrap を最新の公開版へ合わせる。既存リリースがないリポジトリは requiresInitialVersion=true とし、リリース PR の作成を停止する。

1. .release-please-manifest.json の基準バージョンと .github/dependency-release.json の bootstrap を確認する。異なる値を使う場合は、設定変更をレビューして main に取り込む。
2. Actions の Release control を選び、main に対して Run workflow を実行する。
3. operation は reconcile、initial-version は manifest と同じ値を指定する。
4. 実行結果で初期バージョン確認の保留が解除されたことを確認する。

initial-version は基準版の確認である。最初に公開する版は、基準版と変更内容から release-please が決める。確認状態は bot 作成の Release automation state Issue に記録される。

## 依存更新を確認する

minor・patch 更新では署名、変更差分、最新 SHA、CI、保護設定を検証する。条件を満たす場合だけ bot が承認し、squash のタイトルと本文を指定して auto-merge を予約する。CI の再実行で失敗した場合やブランチが更新された場合は、以前の成功結果を使わない。

major、更新種別不明、breaking-change ラベル付きの PR は管理者が確認する。maintain または admin 権限を持つ管理者が最新 SHA を承認すると、次の定期実行または Dependency control の手動実行で再評価する。dependency-review の記録にあるタイトルと本文で squash マージする。

製品の破壊的変更がある場合は、PR 本文に BREAKING CHANGE: と変更内容を記載する。依存先の major 更新だけを理由に製品を major にしない。依存定義以外の変更、署名の不備、API エラーは保留理由を確認して修正する。

通常更新とセキュリティ更新に同じ判定条件を適用する。GitHub 標準の Dependabot 通知から公開時期を判断する。独自のアラート照合、根拠登録、セキュリティ修正ごとのコメント通知は行わない。

## リリース PR を公開する

1. GitHub の通知からリリース PR を開き、対象バージョンと変更履歴を確認する。
2. 最新の main を取り込み、CI が承認待ちなら Approve workflows to run を選ぶ。GITHUB_TOKEN が作成・更新した PR の CI には人の開始承認が必要になる。
3. ci と release-ready の成功、配布対象、互換性を確認して PR をレビューし、手動でマージする。
4. Release control の配布処理が成功し、GitHub Release の draft が解除されたことを確認する。

リリース PR は自動マージしない。main への push がワークフローを起動しない場合も、30 分ごとの定期実行で処理する。即時に処理する場合は Release control を reconcile で手動起動する。

配布処理はリリース PR のマージ SHA を使用する。GoReleaser は既存 draft へ添付し、Windows 配布は ZIP と checksums.txt を添付する。期待する全ファイルと SHA-256 を検証した後に公開する。ソースのみの配布でもタグ、マージ SHA、PR の必須 CI を照合する。

コンテナはバージョン付きタグを先に公開する。既存タグのコミットと版が一致する場合は再利用し、異なる場合は停止する。全配布物の検証後に latest を更新する。途中失敗時には、GitHub Release が draft のままでもバージョン付きイメージを取得できる場合がある。

## 公開を保留して revert する

1. Release control を operation=hold で手動起動する。Release automation state Issue の hold=true と、リリース PR が draft になったことを確認する。
2. 対象変更を revert する。
3. operation=preview で変更履歴を再生成する。この操作ではタグと Release を作成しない。
4. 実際の差分と変更履歴を照合する。リリース対象がなければ PR を閉じる。
5. 対象が残る場合は履歴を確認してから operation=resume を実行し、リリース PR を ready に戻す。

取り消した変更が候補に残る場合は、[release-please のコミット情報の補正](https://github.com/googleapis/release-please#how-can-i-fix-release-notes)を使う。保留状態を保存する Issue は手で編集・削除しない。保留と解除の実行者には maintain または admin 権限が必要となる。

## 途中失敗から復旧する

Release control を reconcile で再実行する。マージ済みの管理対象 PR、バージョンファイル、タグ、既存 draft を照合し、同じ Release ID で配布を再開する。Release 作成後のラベル更新だけが失敗した場合も、既存 Release を再利用する。

タグとマージ SHA の不一致、既存イメージの SHA 不一致、複数の未完了リリースがある場合は処理を停止する。原因を確認するまで保留を維持する。公開済みの配布物を自動で置き換えたり、復旧のためにタグを移動・削除したりしない。

自動化を停止する場合は Dependency control と Release control を無効にし、予約済み auto-merge を解除する。開いているリリース PR は draft に戻す。公開済みタグと Release は維持する。

## 受入検証

導入時に flexi と dd-conf-gen で、通常の依存更新、手動確認が必要な major、CI 失敗、head SHA 更新、保留中の公開拒否を確認する。リリース PR の作成と CI 開始承認、配布失敗後の再実行、既存 draft の再利用も確認する。その後にコンテナ配布と Windows 配布を検証する。

通知は各リポジトリの Watch と個人のメール設定に依存する。リリース PR と Dependabot の通知を管理者が受信できることを確認する。

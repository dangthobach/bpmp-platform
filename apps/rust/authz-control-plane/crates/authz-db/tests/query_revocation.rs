use sqlx::{PgPool, Row};
use uuid::Uuid;

#[sqlx::test(migrations = "../authz-db/migrations")]
async fn revision_and_revoke_feed_commit_and_rollback_together(pool: PgPool) {
    let tenant = Uuid::new_v4();
    let actor = Uuid::new_v4();
    sqlx::query("INSERT INTO tenant(id,code,name) VALUES($1,'revoke','Revoke')")
        .bind(tenant)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query("INSERT INTO user_account(id,tenant_id,username) VALUES($1,$2,'actor')")
        .bind(actor)
        .bind(tenant)
        .execute(&pool)
        .await
        .unwrap();
    let before: i64 =
        sqlx::query_scalar("SELECT revision FROM query_security_revision WHERE singleton")
            .fetch_one(&pool)
            .await
            .unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("UPDATE user_account SET query_revoked=true,query_revoke_epoch=query_revoke_epoch+1 WHERE id=$1").bind(actor).execute(&mut *tx).await.unwrap();
    sqlx::query("INSERT INTO query_revoke_feed(sequence,tenant_id,actor_id,revoke_epoch,revoked,reason,performed_by) SELECT revision,$1,$2,1,true,'test','test' FROM query_security_revision").bind(tenant).bind(actor).execute(&mut *tx).await.unwrap();
    tx.rollback().await.unwrap();
    let after: i64 =
        sqlx::query_scalar("SELECT revision FROM query_security_revision WHERE singleton")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(before, after);
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM query_revoke_feed")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 0);
    let row = sqlx::query("SELECT query_revoke_epoch,query_revoked FROM user_account WHERE id=$1")
        .bind(actor)
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(row.get::<i64, _>("query_revoke_epoch"), 0);
    assert!(!row.get::<bool, _>("query_revoked"));
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("UPDATE user_account SET query_revoked=true,query_revoke_epoch=1 WHERE id=$1")
        .bind(actor)
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("INSERT INTO query_revoke_feed(sequence,tenant_id,actor_id,revoke_epoch,revoked,reason,performed_by) SELECT revision,$1,$2,1,true,'test','test' FROM query_security_revision").bind(tenant).bind(actor).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    assert!(sqlx::query("DELETE FROM query_revoke_feed")
        .execute(&pool)
        .await
        .is_err());
    assert!(sqlx::query("UPDATE query_revoke_feed SET reason='changed'")
        .execute(&pool)
        .await
        .is_err());
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM query_revoke_feed")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 1);
}
